import { useEffect, useRef, useState } from 'react'
import Hls from 'hls.js'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress, ProgressLabel, ProgressValue } from '@/components/ui/progress'

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
      setPhase('watching')
      try {
        setVideo(await completeUpload(created.video.id))
      } catch {
      }
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
        setError(null)
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
      <h1 className="font-heading text-2xl font-semibold">Video Thing</h1>

      <Card>
        <CardHeader>
          <CardTitle>Upload a video</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <label htmlFor="file" className="text-sm font-medium">Video file</label>
          <input
            id="file"
            type="file"
            accept="video/*"
            className="text-sm text-muted-foreground"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
          <Button
            className="self-start"
            disabled={!file || phase === 'uploading' || phase === 'watching'}
            onClick={startUpload}
          >
            Upload
          </Button>

          {phase === 'uploading' && (
            <Progress value={progress}>
              <ProgressLabel>Uploading</ProgressLabel>
              <ProgressValue />
            </Progress>
          )}
          {video && <p className="text-sm text-muted-foreground">Status: {video.status}</p>}
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          {video?.status === 'failed' && (
            <p role="alert" className="text-sm text-destructive">Processing failed.</p>
          )}
        </CardContent>
      </Card>

      {video?.status === 'ready' && video.master_playlist && (
        <Card>
          <CardHeader>
            <CardTitle>{video.title}</CardTitle>
          </CardHeader>
          <CardContent>
            <Player src={video.master_playlist} />
          </CardContent>
        </Card>
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
