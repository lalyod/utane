import { api } from './helper/api.js'

const $ = (selector) => document.querySelector(selector)
const pollEvery = 15000

const jsonOptions = (method, body) => ({
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
})

const describe = (status) => {
    if (status.syncing) return 'Syncing…'
    if (!status.lastRun) return 'Not synced yet.'
    const peers = status.peers
        ? ` · ${status.peers} device${status.peers === 1 ? '' : 's'} on this network`
        : ''
    return `Last synced ${new Date(status.lastRun).toLocaleTimeString()}${peers}`
}

const renderStatus = ({ config, status }, { fillForm }) => {
    $('#sync-status').textContent = describe(status)
    const error = $('#sync-error')
    error.textContent = status.lastError
    error.classList.toggle('hidden', !status.lastError)
    if (!fillForm) return
    const form = $('#sync-form')
    form.elements.folder.value = config.folder
    form.elements.lan.checked = config.lan
    form.elements.groupCode.value = config.groupCode
}

// initSync wires the sync dialog and reloads the library when another device's changes arrive.
export const initSync = ({ load, showMessage }) => {
    const dialog = $('#sync-dialog')
    const form = $('#sync-form')
    let seenChanged = null

    const refresh = async (fillForm = false) => {
        const result = await api('/sync/status')
        if (dialog.open) renderStatus(result, { fillForm })
        const changed = result.status.changed
        // Not while a dialog is open: reloading re-renders lists the user may be editing.
        if (seenChanged !== null && changed !== seenChanged && !document.querySelector('dialog[open]')) {
            await load()
        } else if (seenChanged !== null && changed !== seenChanged) {
            return result // keep seenChanged so the next poll reloads
        }
        seenChanged = changed

        return result
    }

    const poll = () => {
        if (document.visibilityState === 'visible') refresh().catch(() => {})
    }
    poll()
    setInterval(poll, pollEvery)
    document.addEventListener('visibilitychange', poll)

    $('#sync-open').addEventListener('click', async () => {
        $('#sync-error').classList.add('hidden')
        dialog.showModal()
        try {
            await refresh(true)
        } catch (error) {
            showMessage(error.message)
        }
    })

    form.addEventListener('submit', async (event) => {
        event.preventDefault()
        const button = $('#sync-save')
        button.disabled = true
        try {
            const result = await api(
                '/sync/config',
                jsonOptions('PUT', {
                    folder: form.elements.folder.value,
                    lan: form.elements.lan.checked,
                    groupCode: form.elements.groupCode.value,
                })
            )
            renderStatus(result, { fillForm: true })
            showMessage('Sync settings saved.')
        } catch (error) {
            showMessage(error.message)
        } finally {
            button.disabled = false
        }
    })

    $('#sync-run').addEventListener('click', async () => {
        try {
            await api('/sync/run', { method: 'POST' })
            $('#sync-status').textContent = 'Syncing…'
            setTimeout(() => refresh().catch(() => {}), 2000)
        } catch (error) {
            showMessage(error.message)
        }
    })
}
