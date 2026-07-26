import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { uploadFile, createVideo } from './api'

class FakeXHR {
  static last: FakeXHR
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null }
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  status = 200
  method = ''
  url = ''
  headers: Record<string, string> = {}
  body: unknown = null

  constructor() { FakeXHR.last = this }
  open(method: string, url: string) { this.method = method; this.url = url }
  setRequestHeader(k: string, v: string) { this.headers[k] = v }
  send(body: unknown) {
    this.body = body
    queueMicrotask(() => {
      this.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 100 } as ProgressEvent)
      this.onload?.()
    })
  }
}

beforeEach(() => {
  vi.stubGlobal('XMLHttpRequest', FakeXHR)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('uploadFile', () => {
  it('PUTs the file with the signed headers and reports progress', async () => {
    const file = new File(['hello'], 'clip.mp4', { type: 'video/mp4' })
    const progress: number[] = []

    await uploadFile(
      { uploadUrl: 'http://s3.test/raw/abc', method: 'PUT', expiresAt: '', headers: { 'Content-Type': 'application/octet-stream' } },
      file,
      (p) => progress.push(p),
    )

    expect(FakeXHR.last.method).toBe('PUT')
    expect(FakeXHR.last.url).toBe('http://s3.test/raw/abc')
    expect(FakeXHR.last.headers['Content-Type']).toBe('application/octet-stream')
    expect(FakeXHR.last.body).toBe(file)
    expect(progress).toEqual([50])
  })

  it('rejects on a non-2xx response', async () => {
    const file = new File(['hello'], 'clip.mp4')
    const promise = uploadFile(
      { uploadUrl: 'http://s3.test/raw/abc', method: 'PUT', expiresAt: '', headers: {} },
      file,
      () => {},
    )
    FakeXHR.last.status = 403
    FakeXHR.last.onload?.()
    await expect(promise).rejects.toThrow(/403/)
  })
})

describe('createVideo', () => {
  it('posts the title and returns the upload target', async () => {
    const body = { video: { id: 'v1', status: 'uploading' }, upload: { uploadUrl: 'u', method: 'PUT', expiresAt: '', headers: {} } }
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => body })
    vi.stubGlobal('fetch', fetchMock)

    const got = await createVideo('My Clip')

    expect(got).toEqual(body)
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toMatch(/\/videos$/)
    expect(JSON.parse(init.body)).toEqual({ title: 'My Clip' })
  })
})
