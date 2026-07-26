import { useEffect, useRef, useState } from 'react'
import Hls from 'hls.js'

import { completeUpload, createVideo, getVideo, uploadFile, type Video } from './api'

type Phase = 'idle' | 'uploading' | 'watching' | 'done'

export default function App({ pollMs = 2000 }: { pollMs?: number }) {
  const [file, setFile] = useState<File | null>(null)
  const [progress, setProgress] = useState(0)
  const [phase, setPhase] = useState<Phase>('idle')
  const [video, setVideo] = useState<Video | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function startUpload() {
    if (!file) return
    setError(null)
    setPhase('uploading')
    try {
      const created = await createVideo(file.name)
      setVideo(created.video)
      await uploadFile(created.upload, file, setProgress)
      setVideo(await completeUpload(created.video.id))
      setPhase('watching')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPhase('idle')
    }
  }

  useEffect(() => {
    if (phase !== 'watching' || !video) return

    let cancelled = false
    const timer = setInterval(async () => {
      try {
        const latest = await getVideo(video.id)
        if (cancelled) return
        setVideo(latest)
        if (latest.status === 'ready' || latest.status === 'failed') setPhase('done')
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      }
    }, pollMs)

    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [phase, video?.id, pollMs])

  return (
    <main className="mx-auto flex max-w-xl flex-col gap-6 p-8">
      <h1 className="text-2xl font-semibold">Video Thing</h1>

      <div className="flex flex-col gap-3 rounded-lg border p-4">
        <label htmlFor="file" className="text-sm font-medium">Video file</label>
        <input
          id="file"
          type="file"
          accept="video/*"
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />
        <button
          className="rounded bg-black px-4 py-2 text-white disabled:opacity-50"
          disabled={!file || phase === 'uploading' || phase === 'watching'}
          onClick={startUpload}
        >
          Upload
        </button>

        {phase === 'uploading' && (
          <progress className="w-full" value={progress} max={100}>{progress}%</progress>
        )}
        {video && <p className="text-sm text-neutral-600">Status: {video.status}</p>}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        {video?.status === 'failed' && (
          <p role="alert" className="text-sm text-red-600">Processing failed.</p>
        )}
      </div>

      {video?.status === 'ready' && video.master_playlist && (
        <Player src={video.master_playlist} />
      )}
    </main>
  )
}

function Player({ src }: { src: string }) {
  const ref = useRef<HTMLVideoElement>(null)

  useEffect(() => {
    const element = ref.current
    if (!element) return

    if (element.canPlayType('application/vnd.apple.mpegurl')) {
      element.src = src
      return
    }
    if (!Hls.isSupported()) return

    const hls = new Hls()
    hls.loadSource(src)
    hls.attachMedia(element)
    return () => hls.destroy()
  }, [src])

  return <video ref={ref} data-testid="player" data-src={src} controls className="w-full rounded-lg" />
}
