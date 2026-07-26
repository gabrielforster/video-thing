export type VideoStatus = 'uploading' | 'processing' | 'ready' | 'failed'

export interface Video {
  id: string
  title: string
  status: VideoStatus
  duration: number | null
  width: number | null
  height: number | null
  size: number | null
  master_playlist: string | null
  thumbnail: string | null
  created_at: string
  updated_at: string
}

export interface UploadTarget {
  uploadUrl: string
  method: string
  expiresAt: string
  headers?: Record<string, string>
}

export interface CreateVideoResponse {
  video: Video
  upload: UploadTarget
}

const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

async function json<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const body = await response.text()
    throw new Error(`${response.status}: ${body}`)
  }
  return response.json() as Promise<T>
}

export async function createVideo(title: string): Promise<CreateVideoResponse> {
  const response = await fetch(`${API_URL}/videos`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
  })
  return json<CreateVideoResponse>(response)
}

export async function completeUpload(id: string): Promise<Video> {
  return json<Video>(await fetch(`${API_URL}/videos/${id}/complete`, { method: 'POST' }))
}

export async function getVideo(id: string): Promise<Video> {
  return json<Video>(await fetch(`${API_URL}/videos/${id}`))
}

// uploadFile uses XMLHttpRequest rather than fetch because fetch exposes no
// upload progress events. The headers come from the API response: they were
// signed into the presigned URL, so they must be sent verbatim.
export function uploadFile(
  target: UploadTarget,
  file: File,
  onProgress: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(target.method || 'PUT', target.uploadUrl)
    for (const [key, value] of Object.entries(target.headers ?? {})) {
      xhr.setRequestHeader(key, value)
    }
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(Math.round((event.loaded / event.total) * 100))
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve()
      else reject(new Error(`upload failed: ${xhr.status}`))
    }
    xhr.onerror = () => reject(new Error('upload failed: network error'))
    xhr.send(file)
  })
}
