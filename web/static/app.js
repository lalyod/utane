import {
    activeAt,
    centeredScrollTop,
    parseEnhancedLRC,
    scriptLang,
} from './lyrics.mjs'
import { adjacentTrackID, lyricsLayout, viewForPath } from './navigation.mjs'
import { clampTime, parsePlaybackState, resumeTime } from './playback.mjs'
import { trackArtworkURL } from './artwork.mjs'

const state = {
    artists: [],
    albums: [],
    tracks: [],
    currentID: '',
    duration: 0,
    selectedAlbum: '',
    query: '',
    view: viewForPath(location.pathname),
    manageTab: 'artists',
    manageQuery: '',
    lyricsPanelOpen: false,
    panelLyrics: { trackID: '', lines: [], activeLyric: -1, activeWord: -1 },
    editorLyrics: { trackID: '', lines: [], activeLyric: -1, activeWord: -1 },
    pendingDelete: null,
    seeking: false,
    playbackRestored: false,
}

const $ = (selector) => document.querySelector(selector)
const audio = $('#audio')
const playbackKey = 'utane.playback'
const savedPlayback = (() => {
    try {
        return parsePlaybackState(localStorage.getItem(playbackKey))
    } catch {
        return null
    }
})()
const mobileLayout = matchMedia('(max-width: 1023px)')
const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)')
let messageTimer
let coverOperation = 0
let coverImporting = false
let coverController
let trackCoverOperation = 0
let trackCoverImporting = false
let trackCoverController
let avatarOperation = 0
let avatarMutating = false
let avatarController
let avatarRevision = 0
let artistDialogReturnID = ''
let loadOperation = 0
let pendingResumeTime = null
let lastSavedSecond = -1
let lastAudibleVolume = savedPlayback?.volume > 0 ? savedPlayback.volume : 1

const api = async (path, options) => {
    const response = await fetch(path, options)
    if (!response.ok) throw new Error((await response.text()).trim())
    return response.headers.get('content-type')?.includes('application/json')
        ? response.json()
        : null
}

const formatTime = (seconds) => {
    const total = Math.max(0, Math.round(seconds || 0))
    return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

const escapeHTML = (value) =>
    String(value ?? '').replace(
        /[&<>"']/g,
        (char) =>
            ({
                '&': '&amp;',
                '<': '&lt;',
                '>': '&gt;',
                '"': '&quot;',
                "'": '&#39;',
            })[char]
    )

const showMessage = (text) => {
    clearTimeout(messageTimer)
    const message = $('#message')
    message.textContent = text
    message.classList.remove('hidden')
    messageTimer = setTimeout(() => message.classList.add('hidden'), 4000)
}

const persistPlayback = (force = false) => {
    if (!state.currentID || pendingResumeTime !== null) return
    const second = Math.max(0, Math.floor(audio.currentTime || 0))
    if (!force && second === lastSavedSecond) return
    lastSavedSecond = second
    const time = Math.round((audio.currentTime || 0) * 100) / 100
    try {
        localStorage.setItem(
            playbackKey,
            JSON.stringify({
                trackID: state.currentID,
                time,
                volume: audio.volume,
                muted: audio.muted,
            })
        )
    } catch {}
}

const clearPersistedPlayback = () => {
    lastSavedSecond = -1
    try {
        localStorage.removeItem(playbackKey)
    } catch {}
}

const updateVolumeUI = () => {
    const muted = audio.muted || audio.volume === 0
    $('#volume-icon').classList.toggle('hidden', muted)
    $('#muted-icon').classList.toggle('hidden', !muted)
    $('#volume-toggle').setAttribute('aria-label', muted ? 'Unmute' : 'Mute')
    $('#volume').value = audio.volume
}

const emptyState = (title, description, spacing = 'py-14') => `
  <div class="flex w-full flex-col items-center justify-center px-6 text-center ${spacing}">
    <span class="grid h-11 w-11 place-items-center rounded-full bg-white/[0.05] text-stone-600">
      <svg aria-hidden="true" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 18V5l10-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="16" cy="16" r="3"/></svg>
    </span>
    <p class="mt-4 font-medium text-stone-300">${escapeHTML(title)}</p>
    <p class="mt-1.5 max-w-sm text-sm leading-6 text-stone-600">${escapeHTML(description)}</p>
  </div>`

const recordIcon = `
  <div class="grid h-full w-full place-items-center bg-[radial-gradient(circle_at_30%_20%,#71552e,transparent_30%),linear-gradient(145deg,#29251f,#111)] text-amber-200/50">
    <svg aria-hidden="true" class="h-10 w-10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1"><circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="2"/><path d="M12 4a8 8 0 0 1 8 8"/></svg>
  </div>`

const albumFor = (id) => state.albums.find((album) => album.id === id)
const artistFor = (id) => state.artists.find((artist) => artist.id === id)
const trackFor = (id) => state.tracks.find((track) => track.id === id)
const currentTrack = () => trackFor(state.currentID)
const coverURL = (albumID) =>
    albumFor(albumID)?.has_cover ? `/music/albums/${albumID}/cover` : ''
const artworkURL = (track) => trackArtworkURL(track, albumFor(track?.album_id))
const artistInitial = (name) =>
    Array.from(String(name || '').trim())[0]?.toLocaleUpperCase() || '?'
const avatarURL = (artist) =>
    artist?.has_avatar
        ? `/music/artists/${encodeURIComponent(artist.id)}/avatar?v=${avatarRevision}`
        : ''

const artistAvatarHTML = (artist, size) =>
    `<span class="relative grid ${size} shrink-0 place-items-center overflow-hidden rounded-full bg-amber-300/10 font-semibold text-amber-200 ring-1 ring-white/10"><span aria-hidden="true">${escapeHTML(artistInitial(artist.name))}</span>${artist.has_avatar ? `<img data-artist-avatar src="${avatarURL(artist)}" alt="" class="absolute inset-0 h-full w-full object-cover">` : ''}</span>`

const wireAvatarFallbacks = (root) => {
    root.querySelectorAll('img[data-artist-avatar]').forEach((image) =>
        image.addEventListener('error', () => image.remove(), { once: true })
    )
}

const restorePlayback = () => {
    if (!savedPlayback) return
    const track = trackFor(savedPlayback.trackID)
    if (!track) {
        clearPersistedPlayback()
        return
    }

    state.currentID = track.id
    pendingResumeTime = savedPlayback.time
    lastSavedSecond = Math.floor(savedPlayback.time)
    audio.volume = savedPlayback.volume
    audio.muted = savedPlayback.muted
    audio.src = `/music/tracks/${track.id}/audio`
    updateVolumeUI()
}

const filteredTracks = () =>
    state.tracks.filter((track) => {
        if (state.selectedAlbum && track.album_id !== state.selectedAlbum)
            return false
        return (
            !state.query ||
            `${track.title} ${track.artist_name} ${track.album_title}`
                .toLowerCase()
                .includes(state.query)
        )
    })

const fillArtistSelects = () => {
    document.querySelectorAll('select[name="artist_id"]').forEach((select) => {
        const current = select.value
        select.innerHTML = '<option value="">Choose artist</option>'
        state.artists.forEach((artist) =>
            select.add(new Option(artist.name, artist.id))
        )
        if (state.artists.some((artist) => artist.id === current))
            select.value = current
    })
}

const fillAlbumSelect = () => {
    const select = $('#track-form select[name="album_id"]')
    const artistID = $('#track-form select[name="artist_id"]').value
    const current = select.value
    select.innerHTML = '<option value="">No album</option>'
    state.albums
        .filter((album) => !artistID || album.artist_id === artistID)
        .forEach((album) => select.add(new Option(album.title, album.id)))
    if ([...select.options].some((option) => option.value === current))
        select.value = current
}

const renderHero = () => {
    const album = albumFor(state.selectedAlbum)
    const art = $('#hero-art')
    if (!album) {
        $('#hero-title').innerHTML = 'Everything,<br>in one place.'
        $('#hero-meta').textContent =
            `${state.tracks.length} track${state.tracks.length === 1 ? '' : 's'} across ${state.albums.length} album${state.albums.length === 1 ? '' : 's'}.`
        art.innerHTML = recordIcon
        return
    }
    $('#hero-title').textContent = album.title
    $('#hero-meta').textContent =
        `${album.artist_name}${album.year ? `, ${album.year}` : ''} · ${state.tracks.filter((track) => track.album_id === album.id).length} tracks`
    art.innerHTML = coverURL(album.id)
        ? `<img src="${coverURL(album.id)}" alt="" class="h-full w-full object-cover">`
        : recordIcon
}

const renderAlbums = () => {
    const list = $('#album-list')
    if (!state.albums.length) {
        list.innerHTML = `<div class="w-full rounded-2xl border border-dashed border-white/10">${emptyState('No albums yet', 'Albums in your library will appear here.', 'py-8')}</div>`
        return
    }
    list.innerHTML = ''
    state.albums.forEach((album) => {
        const selected = state.selectedAlbum === album.id
        const item = document.createElement('button')
        item.type = 'button'
        item.className = `group w-36 shrink-0 snap-start text-left sm:w-44 ${selected ? 'text-amber-300' : 'text-stone-100'}`
        item.innerHTML = `
      <span class="relative block aspect-square overflow-hidden rounded-2xl border ${selected ? 'border-amber-300/60' : 'border-white/[0.07]'} bg-white/[0.04] shadow-lg shadow-black/20 transition duration-300 group-hover:-translate-y-1">
        ${album.has_cover ? `<img src="/music/albums/${album.id}/cover" alt="" class="h-full w-full object-cover">` : recordIcon}
      </span>
      <span class="mt-3 block truncate text-sm font-semibold">${escapeHTML(album.title)}</span>
      <span class="mt-1 block truncate text-xs text-stone-500">${escapeHTML(album.artist_name)}${album.year ? ` · ${album.year}` : ''}</span>`
        item.addEventListener('click', () => {
            state.selectedAlbum = selected ? '' : album.id
            renderLibrary()
            $('#tracks-heading').scrollIntoView({
                behavior: reducedMotion.matches ? 'auto' : 'smooth',
                block: 'start',
            })
        })
        list.append(item)
    })
}

const renderTracks = () => {
    const tracks = filteredTracks()
    const selected = albumFor(state.selectedAlbum)
    $('#tracks-heading').textContent = selected
        ? selected.title
        : state.query
          ? 'Search results'
          : 'All tracks'
    $('#track-count').textContent =
        `${tracks.length} track${tracks.length === 1 ? '' : 's'}`
    if (!tracks.length) {
        $('#track-list').innerHTML = emptyState(
            state.query ? 'No results' : 'No tracks yet',
            state.query
                ? `Nothing in your library matches "${state.query}".`
                : 'Tracks in your library will appear here.'
        )
        return
    }
    $('#track-list').innerHTML = ''
    tracks.forEach((track, visibleIndex) => {
        const active = track.id === state.currentID
        const button = document.createElement('button')
        button.type = 'button'
        button.className = `group grid w-full grid-cols-[2.5rem_minmax(0,1fr)_3rem] items-center border-b border-white/[0.045] px-4 py-3 text-left transition last:border-0 hover:bg-white/[0.045] md:grid-cols-[3rem_minmax(0,1.4fr)_minmax(8rem,1fr)_5rem] md:px-5 ${active ? 'bg-amber-300/[0.07]' : ''}`
        button.innerHTML = `
      <span class="text-xs tabular-nums ${active ? 'text-amber-300' : 'text-stone-600 group-hover:text-stone-300'}">${active ? '●' : track.track_number || visibleIndex + 1}</span>
      <span class="flex min-w-0 items-center gap-3"><span class="h-10 w-10 shrink-0 overflow-hidden rounded-lg bg-white/[0.05]">${artworkURL(track) ? `<img src="${artworkURL(track)}" alt="" class="h-full w-full object-cover">` : recordIcon}</span><span class="min-w-0"><span class="block truncate text-sm font-medium ${active ? 'text-amber-300' : 'text-stone-100'}">${escapeHTML(track.title)}</span><span class="mt-0.5 block truncate text-xs text-stone-500">${escapeHTML(track.artist_name)}</span></span></span>
      <span class="hidden truncate pr-4 text-sm text-stone-500 md:block">${escapeHTML(track.album_title || 'Single')}</span>
      <span class="text-right text-xs tabular-nums text-stone-600">${formatTime(track.duration)}</span>`
        button.addEventListener('click', () => playTrack(track.id))
        $('#track-list').append(button)
    })
}

const renderLibrary = () => {
    renderHero()
    renderAlbums()
    renderTracks()
}

const actionButtons = (type, id, includeLyrics = false) => `
  ${type === 'track' ? `<button data-action="play" data-type="${type}" data-id="${id}" class="text-amber-300 hover:text-amber-200">Play</button>` : ''}
  <button data-action="edit" data-type="${type}" data-id="${id}" class="text-stone-300 hover:text-white">Edit</button>
  ${includeLyrics ? `<button data-action="lyrics" data-type="${type}" data-id="${id}" class="text-stone-300 hover:text-white">Lyrics</button>` : ''}
  <button data-action="delete" data-type="${type}" data-id="${id}" class="text-red-400 hover:text-red-300">Delete</button>`

const manageItems = () => {
    const items = state[state.manageTab]
    if (!state.manageQuery) return items
    return items.filter((item) =>
        `${item.name || ''} ${item.title || ''} ${item.artist_name || ''} ${item.album_title || ''}`
            .toLowerCase()
            .includes(state.manageQuery)
    )
}

const renderManage = () => {
    document.querySelectorAll('#manage-tabs [data-tab]').forEach((button) => {
        const active = button.dataset.tab === state.manageTab
        button.classList.toggle('bg-white/10', active)
        button.classList.toggle('text-stone-500', !active)
        button.setAttribute('aria-selected', String(active))
    })
    const singular = state.manageTab.slice(0, -1)
    $('#manage-create').textContent =
        state.manageTab === 'tracks' ? 'Upload track' : `Add ${singular}`
    $('#manage-search').placeholder = `Search ${state.manageTab}`
    const items = manageItems()
    if (!items.length) {
        $('#manage-list').innerHTML = emptyState(
            state.manageQuery ? 'No results' : `No ${state.manageTab} yet`,
            state.manageQuery
                ? 'Try a different search.'
                : `Create your first ${singular} to get started.`
        )
        return
    }

    const rows = items
        .map((item) => {
            if (state.manageTab === 'artists')
                return `<tr class="border-b border-white/[0.06] last:border-0"><td class="px-5 py-4 font-medium"><span class="flex items-center gap-3">${artistAvatarHTML(item, 'h-11 w-11 text-sm')}<span>${escapeHTML(item.name)}</span></span></td><td class="px-5 py-4 text-stone-500">${state.albums.filter((album) => album.artist_id === item.id).length} albums · ${state.tracks.filter((track) => track.artist_id === item.id).length} tracks</td><td class="px-5 py-4 text-right text-sm space-x-4">${actionButtons('artist', item.id)}</td></tr>`
            if (state.manageTab === 'albums')
                return `<tr class="border-b border-white/[0.06] last:border-0"><td class="px-5 py-4 font-medium">${escapeHTML(item.title)}</td><td class="px-5 py-4 text-stone-500">${escapeHTML(item.artist_name)}</td><td class="px-5 py-4 text-stone-500">${item.year || '—'}</td><td class="px-5 py-4 text-right text-sm space-x-4">${actionButtons('album', item.id)}</td></tr>`
            return `<tr class="border-b border-white/[0.06] last:border-0"><td class="px-5 py-4 font-medium">${escapeHTML(item.title)}</td><td class="px-5 py-4 text-stone-500">${escapeHTML(item.artist_name)}</td><td class="px-5 py-4 text-stone-500">${escapeHTML(item.album_title || 'Single')}</td><td class="px-5 py-4 text-right text-sm whitespace-nowrap space-x-3">${actionButtons('track', item.id, true)}</td></tr>`
        })
        .join('')
    const headers =
        state.manageTab === 'artists'
            ? '<th class="px-5 py-3">Name</th><th class="px-5 py-3">Library</th>'
            : state.manageTab === 'albums'
              ? '<th class="px-5 py-3">Title</th><th class="px-5 py-3">Artist</th><th class="px-5 py-3">Year</th>'
              : '<th class="px-5 py-3">Title</th><th class="px-5 py-3">Artist</th><th class="px-5 py-3">Album</th>'
    const cards = items
        .map((item) => {
            const title = item.name || item.title
            const detail =
                state.manageTab === 'artists'
                    ? `${state.albums.filter((album) => album.artist_id === item.id).length} albums · ${state.tracks.filter((track) => track.artist_id === item.id).length} tracks`
                    : `${item.artist_name}${state.manageTab === 'tracks' ? ` · ${item.album_title || 'Single'}` : item.year ? ` · ${item.year}` : ''}`
            return `<article class="rounded-2xl border border-white/[0.07] bg-white/[0.025] p-5"><div class="flex items-center gap-3">${state.manageTab === 'artists' ? artistAvatarHTML(item, 'h-12 w-12 text-base') : ''}<div class="min-w-0"><h3 class="truncate font-semibold">${escapeHTML(title)}</h3><p class="mt-1 text-sm text-stone-500">${escapeHTML(detail)}</p></div></div><div class="mt-5 flex flex-wrap gap-4 text-sm">${actionButtons(singular, item.id, state.manageTab === 'tracks')}</div></article>`
        })
        .join('')
    $('#manage-list').innerHTML =
        `<div class="hidden overflow-hidden rounded-2xl border border-white/[0.07] md:block"><table class="w-full text-left"><thead class="bg-white/[0.04] text-xs uppercase tracking-wider text-stone-500"><tr>${headers}<th class="px-5 py-3 text-right">Actions</th></tr></thead><tbody>${rows}</tbody></table></div><div class="grid gap-3 md:hidden">${cards}</div>`
    wireAvatarFallbacks($('#manage-list'))
}

const renderRoute = () => {
    state.view = viewForPath(location.pathname)
    const layout = lyricsLayout(
        state.view,
        state.lyricsPanelOpen,
        mobileLayout.matches
    )
    $('#player-layout').classList.toggle('hidden', state.view !== 'player')
    $('#player-layout').classList.toggle('flex', state.view === 'player')
    $('#player-layout').classList.toggle(
        'lyrics-open',
        layout.panel && !mobileLayout.matches
    )
    $('#player-layout').classList.toggle('max-w-[90rem]', !layout.panel)
    $('#player-view').classList.toggle('hidden', !layout.library)
    $('#now-playing-view').classList.toggle('hidden', !layout.nowPlaying)
    $('#now-playing-view').classList.toggle('flex', layout.nowPlaying)
    $('#lyrics-panel').classList.toggle('hidden', !layout.panel)
    $('#lyrics-panel').classList.toggle('flex', layout.panel)
    $('#manage-view').classList.toggle('hidden', state.view !== 'manage')
    $('#player-search-wrap').classList.toggle(
        'hidden',
        state.view !== 'player' || (layout.panel && mobileLayout.matches)
    )
    $('#lyrics-toggle').setAttribute('aria-expanded', String(layout.panel))
    $('#lyrics-toggle').setAttribute(
        'aria-label',
        layout.panel ? 'Hide lyrics' : 'Show lyrics'
    )
    $('#lyrics-toggle').classList.toggle('text-amber-300', layout.panel)
    document.querySelectorAll('[data-route]').forEach((link) => {
        const active = link.dataset.route === state.view
        if (!link.classList.contains('bg-amber-300')) {
            link.classList.toggle('bg-white/10', active)
            link.classList.toggle('text-white', active)
            link.classList.toggle('text-stone-500', !active)
        }
        if (active) link.setAttribute('aria-current', 'page')
        else link.removeAttribute('aria-current')
    })
    if (state.view === 'manage') renderManage()
    else renderLibrary()
}

const render = () => {
    fillArtistSelects()
    fillAlbumSelect()
    renderLibrary()
    renderManage()
    renderRoute()
}

const load = async () => {
    const operation = ++loadOperation
    const [artists, albums, tracks] = await Promise.all([
        api('/music/artists'),
        api('/music/albums'),
        api('/music/tracks'),
    ])
    if (operation !== loadOperation) return false
    state.artists = artists || []
    state.albums = albums || []
    state.tracks = tracks || []
    if (!state.playbackRestored) {
        state.playbackRestored = true
        restorePlayback()
    }
    if (state.selectedAlbum && !albumFor(state.selectedAlbum))
        state.selectedAlbum = ''
    if (state.currentID && !currentTrack()) clearPlayer()
    else if (currentTrack()) {
        $('#player-title').textContent = currentTrack().title
        $('#player-meta').textContent =
            `${currentTrack().artist_name}${currentTrack().album_title ? ` · ${currentTrack().album_title}` : ''}`
        setPlayerArtwork(currentTrack())
    }
    if (state.lyricsPanelOpen) loadPanelLyrics(currentTrack())
    renderNowPlaying(currentTrack())
    render()
    if ($('#track-dialog').open) updateTrackCoverControls()
    return true
}

const setPlayerArtwork = (track) => {
    $('#player-art').innerHTML = artworkURL(track)
        ? `<img src="${artworkURL(track)}" alt="" class="h-full w-full object-cover">`
        : recordIcon
}

const renderNowPlaying = (track) => {
    $('#now-playing-title').textContent = track?.title || 'Nothing playing'
    $('#now-playing-meta').textContent = track
        ? `${track.artist_name}${track.album_title ? ` · ${track.album_title}` : ''}`
        : 'Choose a track'
    $('#now-playing-art').innerHTML =
        track && artworkURL(track)
            ? `<img src="${artworkURL(track)}" alt="" class="h-full w-full object-cover">`
            : recordIcon
}

const setPlaybackIcon = (playing) => {
    $('#play-icon').classList.toggle('hidden', playing)
    $('#pause-icon').classList.toggle('hidden', !playing)
    $('#play-toggle').setAttribute('aria-label', playing ? 'Pause' : 'Play')
}

const clearPlayer = () => {
    audio.pause()
    audio.removeAttribute('src')
    audio.load()
    state.currentID = ''
    pendingResumeTime = null
    clearPersistedPlayback()
    $('#player-title').textContent = 'Nothing playing'
    $('#player-meta').textContent = 'Choose a track'
    $('#player-art').innerHTML = recordIcon
    $('#seek').value = 0
    $('#seek').max = 0
    $('#current-time').textContent = '0:00'
    $('#total-time').textContent = '0:00'
    setPlaybackIcon(false)
    renderNowPlaying()
    if (state.lyricsPanelOpen) loadPanelLyrics()
}

const playTrack = (id) => {
    const track = trackFor(id)
    if (!track) return
    state.currentID = id
    $('#player-title').textContent = track.title
    $('#player-meta').textContent =
        `${track.artist_name}${track.album_title ? ` · ${track.album_title}` : ''}`
    setPlayerArtwork(track)
    renderNowPlaying(track)
    audio.src = `/music/tracks/${track.id}/audio`
    pendingResumeTime = null
    lastSavedSecond = -1
    persistPlayback(true)
    audio
        .play()
        .catch((error) =>
            showMessage(
                error.message || error.name || 'Playback could not start.'
            )
        )
    renderTracks()
    if (state.lyricsPanelOpen) loadPanelLyrics(track)
}

const playFirstVisible = () => {
    const track = filteredTracks()[0] || state.tracks[0]
    if (track) playTrack(track.id)
    else showMessage('There are no tracks to play.')
}

const setLyricsEditor = (editing) => {
    $('#lyrics-display').classList.toggle('hidden', editing)
    $('#lyrics-editor').classList.toggle('hidden', !editing)
    $('#lyrics-editor').classList.toggle('flex', editing)
}

const renderLyrics = (display, lyrics, emptyTitle, emptyDescription) => {
    display.innerHTML = ''
    display.scrollTop = 0
    lyrics.activeLyric = -1
    lyrics.activeWord = -1
    const language = scriptLang(
        lyrics.lines.map((line) => line.text).join('\n')
    )
    if (language) display.lang = language
    else display.removeAttribute('lang')
    if (!lyrics.lines.length) {
        display.innerHTML = emptyState(emptyTitle, emptyDescription)
        return
    }
    const interactive = display.id === 'lyrics-panel-display'
    lyrics.lines.forEach((line, lineIndex) => {
        const timedWords = interactive && line.words.length > 0
        const paragraph = document.createElement(
            interactive && !timedWords ? 'button' : 'p'
        )
        if (interactive && !timedWords) {
            paragraph.type = 'button'
            paragraph.dataset.time = line.time
        }
        paragraph.dataset.line = lineIndex
        paragraph.className = `my-5 text-xl font-semibold leading-relaxed text-stone-600 transition duration-300 md:text-3xl ${interactive ? 'block w-full rounded-xl text-center hover:text-stone-300 focus:outline-none focus:ring-1 focus:ring-amber-300/50' : ''}`
        if (line.prefix) {
            const prefix = document.createElement(
                timedWords ? 'button' : 'span'
            )
            if (timedWords) {
                prefix.type = 'button'
                prefix.dataset.time = line.time
            }
            prefix.className = `text-stone-500 ${timedWords ? 'rounded focus:outline-none focus:ring-1 focus:ring-amber-300/50' : ''}`
            prefix.textContent = line.prefix
            paragraph.append(prefix)
        }
        if (line.words.length)
            line.words.forEach((word, wordIndex) => {
                const wordElement = document.createElement(
                    interactive ? 'button' : 'span'
                )
                if (interactive) {
                    wordElement.type = 'button'
                    wordElement.dataset.time = word.time
                }
                wordElement.dataset.word = wordIndex
                wordElement.className = `rounded text-stone-500 transition-colors ${interactive ? 'focus:outline-none focus:ring-1 focus:ring-amber-300/50' : ''}`
                wordElement.textContent = word.text
                paragraph.append(wordElement)
            })
        else paragraph.textContent = line.text
        display.append(paragraph)
    })
}

const syncLyricsView = (display, lyrics) => {
    if (lyrics.trackID !== state.currentID || !lyrics.lines.length) return
    const lineIndex = activeAt(lyrics.lines, audio.currentTime)
    if (lineIndex !== lyrics.activeLyric) {
        display.querySelectorAll('[data-word]').forEach((word) => {
            word.classList.remove('text-amber-300', 'text-stone-100')
            word.classList.add('text-stone-500')
        })
        display.querySelectorAll('[data-line]').forEach((line, index) => {
            line.classList.toggle('text-stone-100', index === lineIndex)
            line.classList.toggle('text-stone-600', index !== lineIndex)
        })
        lyrics.activeLyric = lineIndex
        lyrics.activeWord = -1
        const activeLine = display.querySelector(`[data-line="${lineIndex}"]`)
        if (activeLine) {
            const displayRect = display.getBoundingClientRect()
            const lineRect = activeLine.getBoundingClientRect()
            display.scrollTo({
                top: centeredScrollTop(
                    display.scrollTop,
                    displayRect.top,
                    display.clientHeight,
                    lineRect.top,
                    lineRect.height
                ),
                behavior: reducedMotion.matches ? 'auto' : 'smooth',
            })
        }
    }
    const line = lyrics.lines[lineIndex]
    if (!line?.words.length) return
    const wordIndex = activeAt(line.words, audio.currentTime)
    if (wordIndex === lyrics.activeWord) return
    display
        .querySelectorAll(`[data-line="${lineIndex}"] [data-word]`)
        .forEach((word, index) => {
            word.classList.toggle('text-amber-300', index === wordIndex)
            word.classList.toggle('text-stone-100', index < wordIndex)
            word.classList.toggle(
                'text-stone-500',
                index > wordIndex || wordIndex === -1
            )
        })
    lyrics.activeWord = wordIndex
}

const syncLyrics = () => {
    if (state.lyricsPanelOpen)
        syncLyricsView($('#lyrics-panel-display'), state.panelLyrics)
    if ($('#lyrics-dialog').open && $('#lyrics-display').offsetParent)
        syncLyricsView($('#lyrics-display'), state.editorLyrics)
}

const loadPanelLyrics = (track) => {
    $('#lyrics-panel-title').textContent = track
        ? `${track.title} · ${track.artist_name}`
        : 'Nothing playing'
    state.panelLyrics.trackID = track?.id || ''
    state.panelLyrics.lines = parseEnhancedLRC(track?.synced_lyrics || '')
    renderLyrics(
        $('#lyrics-panel-display'),
        state.panelLyrics,
        track ? 'No synchronized lyrics' : 'Nothing playing',
        track
            ? 'No lyrics have been saved for this track.'
            : 'Choose a track from your library to see its lyrics.'
    )
    syncLyrics()
}

const loadEditorLyrics = (track) => {
    $('#lyrics-title').textContent = `${track.title} · ${track.artist_name}`
    $('#lyrics-input').value = track.synced_lyrics || ''
    $('#lyrics-status').textContent = track.synced_lyrics
        ? ''
        : 'No lyrics saved for this track.'
    state.editorLyrics.lines = parseEnhancedLRC(track.synced_lyrics || '')
    setLyricsEditor(false)
    renderLyrics(
        $('#lyrics-display'),
        state.editorLyrics,
        'No synchronized lyrics',
        'Fetch from LRCLIB or add enhanced LRC manually.'
    )
}

const openLyricsEditor = (track) => {
    state.editorLyrics.trackID = track.id
    loadEditorLyrics(track)
    $('#lyrics-dialog').showModal()
    syncLyrics()
}

const setLyricsPanel = (open) => {
    state.lyricsPanelOpen = open
    renderRoute()
    if (open) {
        loadPanelLyrics(currentTrack())
        ;(mobileLayout.matches
            ? $('#lyrics-back')
            : $('#lyrics-panel-close')
        ).focus()
    } else {
        $('#lyrics-toggle').focus()
    }
}

const seekTo = (seconds, save = true) => {
    if (!state.currentID || !Number.isFinite(seconds)) return
    const target = clampTime(seconds, audio.duration)
    $('#seek').value = target
    $('#current-time').textContent = formatTime(target)
    if (!Number.isFinite(audio.duration)) {
        pendingResumeTime = target
        return
    }

    audio.currentTime = target
    syncLyrics()
    if (save) persistPlayback(true)
}

const fetchLyrics = async (track) =>
    (await api(`/music/tracks/${track.id}/lyrics/lookup`)).lyrics
const saveLyrics = async (track, lyrics) => {
    await api(`/music/tracks/${track.id}/lyrics`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ lyrics }),
    })
    track.synced_lyrics = lyrics
}

const lookupLyricsInBackground = async (id) => {
    const track = trackFor(id)
    if (!track || track.synced_lyrics) return
    const lyrics = await fetchLyrics(track)
    if (track.synced_lyrics) return
    await saveLyrics(track, lyrics)
    if (state.lyricsPanelOpen && track.id === state.currentID)
        loadPanelLyrics(track)
}

const resetDialogForm = (form) => {
    form.reset()
    form.dataset.id = ''
}

const resetCoverCandidates = () => {
    coverController?.abort()
    coverController = undefined
    coverOperation += 1
    coverImporting = false
    $('#album-submit').disabled = false
    $('#cover-search').disabled = false
    $('#cover-search').setAttribute('aria-expanded', 'false')
    $('#cover-status').textContent = ''
    $('#cover-candidates').replaceChildren()
    $('#cover-candidates').classList.add('hidden')
}

const importCover = async (candidate) => {
    const dialog = $('#album-dialog')
    const albumID = $('#album-form').dataset.id
    if (!albumID || coverImporting) return

    coverImporting = true
    const operation = ++coverOperation
    coverController?.abort()
    const controller = new AbortController()
    coverController = controller
    $('#cover-search').disabled = true
    $('#album-submit').disabled = true
    $('#cover-status').textContent =
        `Importing “${candidate.title || 'Untitled release'}”...`
    $('#cover-candidates')
        .querySelectorAll('button')
        .forEach((button) => {
            button.disabled = true
        })
    try {
        await api(`/music/albums/${albumID}/cover/fetch`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ release_id: candidate.release_id }),
            signal: controller.signal,
        })
        if (
            operation !== coverOperation ||
            !dialog.open ||
            $('#album-form').dataset.id !== albumID
        )
            return
        coverController = undefined
        try {
            await load()
        } catch {
            coverImporting = false
            $('#album-submit').disabled = false
            $('#cover-search').disabled = false
            $('#cover-candidates')
                .querySelectorAll('button')
                .forEach((button) => {
                    button.disabled = false
                })
            $('#cover-status').textContent =
                'Cover imported, but the library could not refresh. Reload the page to see it.'
            showMessage('Cover imported; library refresh failed.')
            return
        }
        if (
            operation !== coverOperation ||
            !dialog.open ||
            $('#album-form').dataset.id !== albumID
        )
            return
        $('#album-form').elements.cover.value = ''
        $('#album-form').elements.remove_cover.checked = false
        $('#remove-cover-wrap').classList.remove('hidden')
        $('#remove-cover-wrap').classList.add('flex')
        resetCoverCandidates()
        showMessage('Cover imported.')
    } catch (error) {
        if (
            operation !== coverOperation ||
            !dialog.open ||
            $('#album-form').dataset.id !== albumID
        )
            return
        if (coverController === controller) coverController = undefined
        coverImporting = false
        $('#album-submit').disabled = false
        $('#cover-search').disabled = false
        $('#cover-candidates')
            .querySelectorAll('button')
            .forEach((button) => {
                button.disabled = false
            })
        $('#cover-status').textContent = error.message || 'Cover import failed.'
    }
}

const renderCoverCandidates = (
    candidates,
    grid = $('#cover-candidates'),
    importCandidate = importCover
) => {
    grid.replaceChildren()
    candidates.forEach((candidate) => {
        const title = String(candidate.title || 'Untitled release')
        const artist = String(candidate.artist || 'Unknown artist')
        const year = candidate.year ? String(candidate.year) : 'Year unknown'
        const button = document.createElement('button')
        button.type = 'button'
        button.className =
            'group overflow-hidden rounded-xl border border-white/10 bg-black/20 text-left transition hover:border-amber-300/50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-amber-300 disabled:cursor-wait disabled:opacity-50'
        button.setAttribute(
            'aria-label',
            `Import cover for ${title} by ${artist}, ${year}`
        )

        const artwork = document.createElement('span')
        artwork.className =
            'grid aspect-square place-items-center overflow-hidden bg-white/[0.04] text-xs text-stone-600'
        if (candidate.thumbnail) {
            const image = document.createElement('img')
            image.src = String(candidate.thumbnail)
            image.alt = ''
            image.loading = 'lazy'
            image.className =
                'h-full w-full object-cover transition group-hover:scale-[1.02]'
            artwork.append(image)
        } else {
            artwork.textContent = 'No preview'
        }

        const details = document.createElement('span')
        details.className = 'block p-3'
        const titleElement = document.createElement('span')
        titleElement.className =
            'block truncate text-sm font-semibold text-stone-100'
        titleElement.textContent = title
        const artistElement = document.createElement('span')
        artistElement.className = 'mt-1 block truncate text-xs text-stone-400'
        artistElement.textContent = artist
        const yearElement = document.createElement('span')
        yearElement.className = 'mt-1 block text-xs text-stone-600'
        yearElement.textContent = year
        details.append(titleElement, artistElement, yearElement)
        button.append(artwork, details)
        button.addEventListener('click', () => importCandidate(candidate))
        grid.append(button)
    })
    grid.classList.remove('hidden')
}

const setTrackCoverBusy = (busy) => {
    trackCoverImporting = busy
    $('#track-submit').disabled = busy
    $('#track-form').elements.artist_id.disabled = busy
    $('#track-form').elements.album_id.disabled = busy
    $('#track-cover-search').disabled = busy
    $('#track-cover-remove').disabled = busy
    $('#track-cover-candidates')
        .querySelectorAll('button')
        .forEach((button) => {
            button.disabled = busy
        })
}

const resetTrackCoverCandidates = () => {
    trackCoverController?.abort()
    trackCoverController = undefined
    trackCoverOperation += 1
    setTrackCoverBusy(false)
    $('#track-cover-search').setAttribute('aria-expanded', 'false')
    $('#track-cover-status').textContent = ''
    $('#track-cover-candidates').replaceChildren()
    $('#track-cover-candidates').classList.add('hidden')
}

const updateAvatarUploadButton = () => {
    $('#avatar-upload').disabled =
        avatarMutating || !$('#avatar-file').files.length
}

const setAvatarBusy = (busy) => {
    avatarMutating = busy
    $('#artist-submit').disabled = busy
    $('#avatar-section')
        .querySelectorAll('[data-avatar-control]')
        .forEach((control) => {
            control.disabled = busy
        })
    if (!busy) updateAvatarUploadButton()
}

const resetAvatarFlow = () => {
    if (!avatarMutating) {
        avatarController?.abort()
        avatarController = undefined
        avatarOperation += 1
    }
    $('#avatar-status').textContent = ''
    if (!avatarMutating) setAvatarBusy(false)
}

const renderArtistAvatar = (artist) => {
    const preview = $('#avatar-preview')
    preview.replaceChildren()
    const initial = document.createElement('span')
    initial.setAttribute('aria-hidden', 'true')
    initial.textContent = artistInitial(artist.name)
    preview.append(initial)
    if (artist.has_avatar) {
        const image = document.createElement('img')
        image.src = avatarURL(artist)
        image.alt = `${artist.name} avatar`
        image.className = 'absolute inset-0 h-full w-full object-cover'
        image.addEventListener('error', () => image.remove(), { once: true })
        preview.classList.add('relative')
        preview.append(image)
    }

    $('#avatar-remove').classList.toggle('hidden', !artist.has_avatar)
}

const reconcileAvatar = (artistID, action) => {
    const artist = artistFor(artistID)
    if (!artist) return
    artist.has_avatar = action !== 'remove'
    renderManage()
}

const restoreAvatarFocus = (target) => {
    const fallback = $('#avatar-file')
    const control =
        target?.isConnected &&
        !target.disabled &&
        !target.classList.contains('hidden')
            ? target
            : fallback
    requestAnimationFrame(() => control.focus())
}

const focusArtistEdit = (artistID) => {
    if (!artistID) return
    const button = [
        ...document.querySelectorAll(
            '[data-action="edit"][data-type="artist"]'
        ),
    ].find((candidate) => candidate.dataset.id === artistID)
    if (button) requestAnimationFrame(() => button.focus())
}

const mutateAvatar = async (action, focusTarget = document.activeElement) => {
    const dialog = $('#artist-dialog')
    const artistID = $('#artist-form').dataset.id
    if (!artistID || avatarMutating) return

    const operation = ++avatarOperation
    avatarController?.abort()
    const controller = new AbortController()
    avatarController = controller
    setAvatarBusy(true)
    const labels = {
        upload: 'Uploading avatar...',
        remove: 'Removing avatar...',
    }
    const completed = { upload: 'Avatar uploaded.', remove: 'Avatar removed.' }
    $('#avatar-status').textContent = labels[action]

    let mutationSucceeded = false
    try {
        const options = {
            method: action === 'remove' ? 'DELETE' : 'PUT',
            signal: controller.signal,
        }
        if (action === 'upload') {
            options.body = new FormData()
            options.body.set('avatar', $('#avatar-file').files[0])
        }
        await api(`/music/artists/${artistID}/avatar`, options)
        mutationSucceeded = true
        avatarRevision += 1
        try {
            if (!(await load()))
                throw new Error('A newer library refresh superseded this one.')
        } catch {
            reconcileAvatar(artistID, action)
            if (
                operation === avatarOperation &&
                dialog.open &&
                $('#artist-form').dataset.id === artistID
            ) {
                renderArtistAvatar(artistFor(artistID))
                $('#avatar-status').textContent =
                    `${completed[action]} The library could not refresh; reload the page if other details look stale.`
            }
            showMessage(`${completed[action]} Library refresh failed.`)
            return
        }
        if (
            operation === avatarOperation &&
            dialog.open &&
            $('#artist-form').dataset.id === artistID
        ) {
            renderArtistAvatar(artistFor(artistID))
            $('#avatar-file').value = ''
            $('#avatar-status').textContent = completed[action]
        }
        showMessage(completed[action])
    } catch (error) {
        if (operation !== avatarOperation || error.name === 'AbortError') return
        const message = error.message || `Avatar ${action} failed.`
        if (dialog.open && $('#artist-form').dataset.id === artistID)
            $('#avatar-status').textContent = message
        showMessage(message)
    } finally {
        if (avatarController === controller) avatarController = undefined
        if (operation === avatarOperation) {
            setAvatarBusy(false)
            if (dialog.open && $('#artist-form').dataset.id === artistID) {
                if (mutationSucceeded && artistFor(artistID))
                    renderArtistAvatar(artistFor(artistID))
                restoreAvatarFocus(focusTarget)
            } else if (!dialog.open) {
                focusArtistEdit(artistID)
            }
        }
    }
}

const updateTrackCoverControls = () => {
    const form = $('#track-form')
    const track = trackFor(form.dataset.id)
    const visible =
        trackCoverImporting ||
        Boolean(
            track &&
            (track.has_cover ||
                (!track.album_id && !form.elements.album_id.value))
        )
    $('#track-cover-online').classList.toggle('hidden', !visible)
    $('#track-cover-remove').classList.toggle('hidden', !track?.has_cover)
    if (!visible && !trackCoverImporting) resetTrackCoverCandidates()
}

const changeTrackCover = async (candidate) => {
    const dialog = $('#track-dialog')
    const trackID = $('#track-form').dataset.id
    if (!trackID || trackCoverImporting) return

    const importing = Boolean(candidate)
    const operation = ++trackCoverOperation
    trackCoverController?.abort()
    const controller = new AbortController()
    trackCoverController = controller
    setTrackCoverBusy(true)
    $('#track-cover-status').textContent = importing
        ? `Importing “${candidate.title || 'Untitled release'}”...`
        : 'Removing track cover...'
    try {
        await api(
            `/music/tracks/${trackID}/cover${importing ? '/fetch' : ''}`,
            {
                method: importing ? 'POST' : 'DELETE',
                headers: importing
                    ? { 'Content-Type': 'application/json' }
                    : undefined,
                body: importing
                    ? JSON.stringify({ release_id: candidate.release_id })
                    : undefined,
                signal: controller.signal,
            }
        )
        if (
            operation !== trackCoverOperation ||
            !dialog.open ||
            $('#track-form').dataset.id !== trackID
        )
            return
        trackCoverController = undefined
        try {
            await load()
        } catch {
            setTrackCoverBusy(false)
            $('#track-cover-status').textContent =
                `Track cover ${importing ? 'imported' : 'removed'}, but the library could not refresh. Reload the page to see it.`
            showMessage('Track cover updated; library refresh failed.')
            return
        }
        if (
            operation !== trackCoverOperation ||
            !dialog.open ||
            $('#track-form').dataset.id !== trackID
        )
            return
        resetTrackCoverCandidates()
        updateTrackCoverControls()
        ;($('#track-cover-online').classList.contains('hidden')
            ? $('#track-submit')
            : $('#track-cover-search')
        ).focus()
        showMessage(
            importing ? 'Track cover imported.' : 'Track cover removed.'
        )
    } catch (error) {
        if (
            operation !== trackCoverOperation ||
            !dialog.open ||
            $('#track-form').dataset.id !== trackID
        )
            return
        if (trackCoverController === controller)
            trackCoverController = undefined
        setTrackCoverBusy(false)
        $('#track-cover-status').textContent =
            error.message ||
            `Track cover ${importing ? 'import' : 'removal'} failed.`
    }
}

const openArtistDialog = (artist) => {
    const form = $('#artist-form')
    resetDialogForm(form)
    resetAvatarFlow()
    artistDialogReturnID = artist?.id || ''
    if (artist) {
        form.dataset.id = artist.id
        form.elements.name.value = artist.name
    }
    $('#avatar-section').classList.toggle('hidden', !artist)
    if (artist) renderArtistAvatar(artist)
    setAvatarBusy(avatarMutating)
    $('#artist-dialog-title').textContent = artist
        ? 'Edit artist'
        : 'Add an artist'
    $('#artist-submit').textContent = artist ? 'Save artist' : 'Add artist'
    $('#artist-dialog').showModal()
}

const openAlbumDialog = (album) => {
    const form = $('#album-form')
    resetDialogForm(form)
    resetCoverCandidates()
    fillArtistSelects()
    form.elements.artist_id.disabled = Boolean(album)
    $('#cover-online').classList.toggle('hidden', !album)
    $('#remove-cover-wrap').classList.toggle('hidden', !album?.has_cover)
    $('#remove-cover-wrap').classList.toggle('flex', Boolean(album?.has_cover))
    if (album) {
        form.dataset.id = album.id
        form.elements.artist_id.value = album.artist_id
        form.elements.title.value = album.title
        form.elements.year.value = album.year || ''
    }
    $('#album-dialog-title').textContent = album
        ? 'Edit album'
        : 'Create an album'
    $('#album-submit').textContent = album ? 'Save album' : 'Create album'
    $('#album-dialog').showModal()
}

const openTrackDialog = (track) => {
    const form = $('#track-form')
    resetDialogForm(form)
    resetTrackCoverCandidates()
    fillArtistSelects()
    state.duration = track?.duration || 0
    form.elements.audio.required = !track
    if (track) {
        form.dataset.id = track.id
        form.elements.artist_id.value = track.artist_id
        fillAlbumSelect()
        form.elements.album_id.value = track.album_id || ''
        form.elements.title.value = track.title
        form.elements.track_number.value = track.track_number || ''
    } else fillAlbumSelect()
    updateTrackCoverControls()
    $('#track-dialog-title').textContent = track
        ? 'Edit track'
        : 'Upload a track'
    $('#track-submit').textContent = track ? 'Save track' : 'Upload track'
    $('#audio-label').textContent = track
        ? 'Replace audio (optional)'
        : 'Audio file'
    $('#duration-label').textContent = track
        ? `${formatTime(track.duration)} · Current audio retained unless replaced.`
        : 'Duration will be read automatically.'
    $('#track-dialog').showModal()
}

const openDelete = (type, item) => {
    state.pendingDelete = { type, id: item.id }
    $('#confirm-title').textContent = `Delete ${type}?`
    const impact =
        type === 'artist'
            ? `Delete “${item.name}”? Artists with albums or tracks cannot be deleted; any conflict from the server will be shown here.`
            : type === 'album'
              ? `Delete “${item.title}”? Its tracks become singles. The tracks and audio remain in your library.`
              : `Delete “${item.title}”? Its audio file will be removed and cannot be recovered.`
    $('#confirm-impact').textContent = impact
    $('#confirm-impact').classList.remove('text-red-400')
    $('#confirm-dialog').showModal()
}

document.querySelectorAll('[data-route]').forEach((link) =>
    link.addEventListener('click', (event) => {
        event.preventDefault()
        history.pushState({}, '', link.getAttribute('href'))
        renderRoute()
    })
)
window.addEventListener('popstate', renderRoute)
mobileLayout.addEventListener('change', renderRoute)

document
    .querySelectorAll('[data-close]')
    .forEach((button) =>
        button.addEventListener('click', () => button.closest('dialog').close())
    )
document.querySelectorAll('dialog').forEach((dialog) =>
    dialog.addEventListener('click', (event) => {
        if (event.target === dialog) dialog.close()
    })
)
$('#album-dialog').addEventListener('close', resetCoverCandidates)
$('#artist-dialog').addEventListener('close', () => {
    const returnID = artistDialogReturnID
    resetAvatarFlow()
    focusArtistEdit(returnID)
})
$('#track-dialog').addEventListener('close', () => {
    const mutationWasRunning = trackCoverImporting
    resetTrackCoverCandidates()
    if (mutationWasRunning) load().catch(() => {})
})

document.querySelectorAll('[data-all-music]').forEach((button) =>
    button.addEventListener('click', () => {
        state.selectedAlbum = ''
        state.query = ''
        $('#search').value = ''
        renderLibrary()
    })
)
$('#search').addEventListener('input', (event) => {
    state.query = event.currentTarget.value.trim().toLowerCase()
    renderTracks()
})
$('#hero-play').addEventListener('click', playFirstVisible)
$('#lyrics-toggle').addEventListener('click', () => {
    const open = state.view === 'manage' || !state.lyricsPanelOpen
    if (state.view === 'manage') history.pushState({}, '', '/')
    setLyricsPanel(open)
})
$('#lyrics-back').addEventListener('click', () => setLyricsPanel(false))
$('#lyrics-panel-close').addEventListener('click', () => setLyricsPanel(false))
$('#lyrics-panel-display').addEventListener('click', (event) => {
    const lyric = event.target.closest('[data-time]')
    if (lyric) seekTo(Number(lyric.dataset.time))
})

$('#manage-tabs').addEventListener('click', (event) => {
    const button = event.target.closest('[data-tab]')
    if (!button) return
    state.manageTab = button.dataset.tab
    state.manageQuery = ''
    $('#manage-search').value = ''
    renderManage()
})
$('#manage-search').addEventListener('input', (event) => {
    state.manageQuery = event.currentTarget.value.trim().toLowerCase()
    renderManage()
})
$('#manage-create').addEventListener('click', () => {
    if (state.manageTab === 'artists') openArtistDialog()
    else if (state.manageTab === 'albums') openAlbumDialog()
    else openTrackDialog()
})

$('#manage-list').addEventListener('click', (event) => {
    const button = event.target.closest('[data-action]')
    if (!button) return
    const item =
        button.dataset.type === 'artist'
            ? state.artists.find((artist) => artist.id === button.dataset.id)
            : button.dataset.type === 'album'
              ? albumFor(button.dataset.id)
              : trackFor(button.dataset.id)
    if (!item) return
    if (button.dataset.action === 'play') playTrack(item.id)
    if (button.dataset.action === 'lyrics') openLyricsEditor(item)
    if (button.dataset.action === 'delete')
        openDelete(button.dataset.type, item)
    if (button.dataset.action === 'edit') {
        if (button.dataset.type === 'artist') openArtistDialog(item)
        else if (button.dataset.type === 'album') openAlbumDialog(item)
        else openTrackDialog(item)
    }
})

$('#lyrics-edit').addEventListener('click', () => setLyricsEditor(true))
$('#lyrics-cancel').addEventListener('click', () => {
    $('#lyrics-input').value =
        trackFor(state.editorLyrics.trackID)?.synced_lyrics || ''
    setLyricsEditor(false)
})
$('#lyrics-fetch').addEventListener('click', async () => {
    const track = trackFor(state.editorLyrics.trackID)
    if (!track) return
    const trackID = track.id
    $('#lyrics-status').textContent = 'Searching LRCLIB...'
    try {
        const lyrics = await fetchLyrics(track)
        if (!$('#lyrics-dialog').open || state.editorLyrics.trackID !== trackID)
            return
        $('#lyrics-input').value = lyrics
        $('#lyrics-status').textContent = 'Lyrics found. Review and save them.'
        setLyricsEditor(true)
    } catch (error) {
        $('#lyrics-status').textContent = error.message
    }
})
$('#lyrics-save').addEventListener('click', async () => {
    const track = trackFor(state.editorLyrics.trackID)
    if (!track) return
    try {
        const lyrics = $('#lyrics-input').value.trim()
        await saveLyrics(track, lyrics)
        state.editorLyrics.lines = parseEnhancedLRC(lyrics)
        $('#lyrics-status').textContent = lyrics
            ? 'Lyrics saved.'
            : 'Lyrics cleared.'
        setLyricsEditor(false)
        renderLyrics(
            $('#lyrics-display'),
            state.editorLyrics,
            'No synchronized lyrics',
            'Fetch from LRCLIB or add enhanced LRC manually.'
        )
        if (state.lyricsPanelOpen && track.id === state.currentID)
            loadPanelLyrics(track)
        else syncLyrics()
    } catch (error) {
        $('#lyrics-status').textContent = error.message
    }
})

$('#artist-form').addEventListener('submit', async (event) => {
    event.preventDefault()
    const form = event.currentTarget
    const editing = Boolean(form.dataset.id)
    if (avatarMutating) {
        showMessage('Wait for the avatar update to finish.')
        return
    }
    try {
        await api(
            editing ? `/music/artists/${form.dataset.id}` : '/music/artists',
            {
                method: editing ? 'PATCH' : 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: form.elements.name.value.trim() }),
            }
        )
        form.closest('dialog').close()
        await load()
        showMessage(
            editing ? 'Artist updated.' : 'Artist added to your library.'
        )
    } catch (error) {
        showMessage(error.message)
    }
})

$('#avatar-file').addEventListener('change', updateAvatarUploadButton)
$('#avatar-upload').addEventListener('click', (event) => {
    if ($('#avatar-file').files.length)
        mutateAvatar('upload', event.currentTarget)
})
$('#avatar-remove').addEventListener('click', (event) =>
    mutateAvatar('remove', event.currentTarget)
)

$('#album-form').addEventListener('submit', async (event) => {
    event.preventDefault()
    const form = event.currentTarget
    const editing = Boolean(form.dataset.id)
    if (coverImporting) {
        showMessage('Wait for the cover import to finish.')
        return
    }
    try {
        await api(
            editing ? `/music/albums/${form.dataset.id}` : '/music/albums',
            { method: editing ? 'PATCH' : 'POST', body: new FormData(form) }
        )
        form.closest('dialog').close()
        await load()
        showMessage(editing ? 'Album updated.' : 'Album created.')
    } catch (error) {
        showMessage(error.message)
    }
})

$('#album-form input[name="cover"]').addEventListener('change', (event) => {
    if (event.currentTarget.files.length)
        event.currentTarget.form.elements.remove_cover.checked = false
})
$('#album-form input[name="remove_cover"]').addEventListener(
    'change',
    (event) => {
        if (event.currentTarget.checked)
            event.currentTarget.form.elements.cover.value = ''
    }
)

$('#cover-search').addEventListener('click', async () => {
    const dialog = $('#album-dialog')
    const albumID = $('#album-form').dataset.id
    if (!albumID || coverImporting) return
    const operation = ++coverOperation
    coverController?.abort()
    const controller = new AbortController()
    coverController = controller
    $('#cover-search').disabled = true
    $('#cover-search').setAttribute('aria-expanded', 'true')
    $('#cover-status').textContent = 'Searching for covers...'
    $('#cover-candidates').replaceChildren()
    $('#cover-candidates').classList.remove('hidden')
    try {
        const candidates = await api(`/music/albums/${albumID}/cover/search`, {
            signal: controller.signal,
        })
        if (
            operation !== coverOperation ||
            !dialog.open ||
            $('#album-form').dataset.id !== albumID
        )
            return
        coverController = undefined
        if (!Array.isArray(candidates))
            throw new Error('Cover search returned an invalid response.')
        renderCoverCandidates(candidates)
        $('#cover-status').textContent = candidates.length
            ? `Found ${candidates.length} cover candidate${candidates.length === 1 ? '' : 's'}. Select one to import.`
            : 'No cover candidates found.'
    } catch (error) {
        if (
            operation !== coverOperation ||
            !dialog.open ||
            $('#album-form').dataset.id !== albumID
        )
            return
        $('#cover-status').textContent = error.message || 'Cover search failed.'
    } finally {
        if (coverController === controller) coverController = undefined
        if (
            operation === coverOperation &&
            dialog.open &&
            $('#album-form').dataset.id === albumID
        )
            $('#cover-search').disabled = false
    }
})

$('#track-cover-search').addEventListener('click', async () => {
    const dialog = $('#track-dialog')
    const trackID = $('#track-form').dataset.id
    if (!trackID || trackCoverImporting) return
    const operation = ++trackCoverOperation
    trackCoverController?.abort()
    const controller = new AbortController()
    trackCoverController = controller
    $('#track-cover-search').disabled = true
    $('#track-cover-search').setAttribute('aria-expanded', 'true')
    $('#track-cover-status').textContent = 'Searching for covers...'
    $('#track-cover-candidates').replaceChildren()
    $('#track-cover-candidates').classList.remove('hidden')
    try {
        const candidates = await api(`/music/tracks/${trackID}/cover/search`, {
            signal: controller.signal,
        })
        if (
            operation !== trackCoverOperation ||
            !dialog.open ||
            $('#track-form').dataset.id !== trackID
        )
            return
        trackCoverController = undefined
        if (!Array.isArray(candidates))
            throw new Error('Cover search returned an invalid response.')
        renderCoverCandidates(
            candidates,
            $('#track-cover-candidates'),
            changeTrackCover
        )
        $('#track-cover-status').textContent = candidates.length
            ? `Found ${candidates.length} cover candidate${candidates.length === 1 ? '' : 's'}. Select one to import.`
            : 'No cover candidates found.'
    } catch (error) {
        if (
            operation !== trackCoverOperation ||
            !dialog.open ||
            $('#track-form').dataset.id !== trackID
        )
            return
        $('#track-cover-status').textContent =
            error.message || 'Cover search failed.'
    } finally {
        if (trackCoverController === controller)
            trackCoverController = undefined
        if (
            operation === trackCoverOperation &&
            dialog.open &&
            $('#track-form').dataset.id === trackID
        )
            $('#track-cover-search').disabled = false
    }
})
$('#track-cover-remove').addEventListener('click', () => changeTrackCover())

$('#track-form select[name="artist_id"]').addEventListener('change', () => {
    fillAlbumSelect()
    updateTrackCoverControls()
})
$('#track-form select[name="album_id"]').addEventListener(
    'change',
    updateTrackCoverControls
)
$('#track-form input[name="audio"]').addEventListener('change', (event) => {
    const input = event.currentTarget
    const file = input.files[0]
    const existing = trackFor(input.form.dataset.id)
    state.duration = existing?.duration || 0
    $('#duration-label').textContent = existing
        ? `${formatTime(existing.duration)} · Current audio retained unless replaced.`
        : 'Duration will be read automatically.'
    if (!file) return
    state.duration = 0
    $('#duration-label').textContent = 'Reading audio duration...'
    const metadata = document.createElement('audio')
    metadata.preload = 'metadata'
    metadata.onloadedmetadata = () => {
        URL.revokeObjectURL(metadata.src)
        if (input.files[0] !== file) return
        state.duration = Number.isFinite(metadata.duration)
            ? Math.round(metadata.duration)
            : 0
        $('#duration-label').textContent = state.duration
            ? `${formatTime(state.duration)} · ${file.name}`
            : "Could not read this file's duration."
    }
    metadata.onerror = () => {
        URL.revokeObjectURL(metadata.src)
        if (input.files[0] !== file) return
        state.duration = 0
        $('#duration-label').textContent =
            "Could not read this file's duration."
    }
    metadata.src = URL.createObjectURL(file)
})

$('#track-form').addEventListener('submit', async (event) => {
    event.preventDefault()
    const form = event.currentTarget
    const editing = Boolean(form.dataset.id)
    if (trackCoverImporting) {
        showMessage('Wait for the cover import to finish.')
        return
    }
    if (!state.duration) {
        showMessage('Wait for the audio duration to load.')
        return
    }
    try {
        const data = new FormData(form)
        data.set('duration', String(state.duration))
        const replacedActive =
            editing &&
            form.dataset.id === state.currentID &&
            form.elements.audio.files.length > 0
        const result = await api(
            editing ? `/music/tracks/${form.dataset.id}` : '/music/tracks',
            { method: editing ? 'PATCH' : 'POST', body: data }
        )
        const id = form.dataset.id || result.id
        form.closest('dialog').close()
        state.duration = 0
        await load()
        if (replacedActive) playTrack(id)
        showMessage(editing ? 'Track updated.' : 'Track uploaded.')
        if (!editing) lookupLyricsInBackground(id).catch(() => {})
    } catch (error) {
        showMessage(error.message)
    }
})

$('#confirm-delete').addEventListener('click', async () => {
    if (!state.pendingDelete) return
    const { type, id } = state.pendingDelete
    try {
        await api(`/music/${type}s/${id}`, { method: 'DELETE' })
        if (type === 'track' && id === state.currentID) clearPlayer()
        $('#confirm-dialog').close()
        state.pendingDelete = null
        await load()
        showMessage(`${type[0].toUpperCase()}${type.slice(1)} deleted.`)
    } catch (error) {
        $('#confirm-impact').textContent = error.message
        $('#confirm-impact').classList.add('text-red-400')
    }
})

$('#play-toggle').addEventListener('click', () => {
    if (!audio.src) playFirstVisible()
    else if (audio.paused)
        audio.play().catch(() => showMessage('Playback could not start.'))
    else audio.pause()
})
$('#prev').addEventListener('click', () => {
    if (audio.currentTime > 3) audio.currentTime = 0
    else {
        const id = adjacentTrackID(state.tracks, state.currentID, -1)
        if (id) playTrack(id)
    }
})
$('#next').addEventListener('click', () => {
    const id = adjacentTrackID(state.tracks, state.currentID, 1)
    if (id) playTrack(id)
})
$('#volume-toggle').addEventListener('click', () => {
    if (audio.muted || audio.volume === 0) {
        if (audio.volume === 0) audio.volume = lastAudibleVolume
        audio.muted = false
    } else {
        audio.muted = true
    }
})
$('#volume').addEventListener('input', (event) => {
    const volume = event.currentTarget.valueAsNumber
    audio.volume = volume
    audio.muted = volume === 0
    if (volume > 0) lastAudibleVolume = volume
})

const seek = $('#seek')
seek.addEventListener('pointerdown', () => {
    state.seeking = true
})
seek.addEventListener('input', (event) => {
    state.seeking = true
    seekTo(event.currentTarget.valueAsNumber, false)
})
seek.addEventListener('change', (event) => {
    seekTo(event.currentTarget.valueAsNumber)
    state.seeking = false
})
seek.addEventListener('pointerup', () => {
    seekTo(seek.valueAsNumber)
    state.seeking = false
})
seek.addEventListener('pointercancel', () => {
    state.seeking = false
    seek.value = audio.currentTime
    $('#current-time').textContent = formatTime(audio.currentTime)
})
seek.addEventListener('blur', () => {
    state.seeking = false
})

audio.addEventListener('play', () => setPlaybackIcon(true))
audio.addEventListener('pause', () => {
    setPlaybackIcon(false)
    persistPlayback(true)
})
audio.addEventListener('ended', () => {
    const id = adjacentTrackID(state.tracks, state.currentID, 1)
    if (id) playTrack(id)
    else setPlaybackIcon(false)
})
audio.addEventListener('error', () =>
    showMessage(`Playback error ${audio.error?.code || 'unknown'}.`)
)
audio.addEventListener('loadedmetadata', () => {
    $('#seek').max = audio.duration || 0
    $('#total-time').textContent = formatTime(audio.duration)
    if (pendingResumeTime !== null) {
        const target = resumeTime(pendingResumeTime, audio.duration)
        pendingResumeTime = null
        audio.currentTime = target
        $('#seek').value = target
        $('#current-time').textContent = formatTime(target)
        persistPlayback(true)
        syncLyrics()
    }
})
audio.addEventListener('timeupdate', () => {
    if (!state.seeking) {
        $('#seek').value = audio.currentTime
        $('#current-time').textContent = formatTime(audio.currentTime)
    }
    syncLyrics()
    persistPlayback()
})
audio.addEventListener('volumechange', () => {
    if (audio.volume > 0) lastAudibleVolume = audio.volume
    updateVolumeUI()
    persistPlayback(true)
})
window.addEventListener('beforeunload', () => persistPlayback(true))

updateVolumeUI()
renderRoute()
load().catch((error) => {
    $('#track-list').innerHTML = emptyState(
        'Library unavailable',
        'The music service could not be reached. Try again shortly.'
    )
    $('#manage-list').innerHTML = emptyState(
        'Library unavailable',
        'The music service could not be reached. Try again shortly.'
    )
    showMessage(error.message)
})
