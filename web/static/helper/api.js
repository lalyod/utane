export const api = async (path, options) => {
    const response = await fetch(path, options)
    if (!response.ok) throw new Error((await response.text()).trim())
    return response.headers.get('content-type')?.includes('application/json')
        ? response.json()
        : null
}
