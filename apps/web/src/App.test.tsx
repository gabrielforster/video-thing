import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'

import App from './App'
import * as api from './api'
import type { Video } from './api'

vi.mock('./api', async () => {
  const actual = await vi.importActual<typeof api>('./api')
  return { ...actual, createVideo: vi.fn(), uploadFile: vi.fn(), completeUpload: vi.fn(), getVideo: vi.fn() }
})

function video(overrides: Partial<Video>): Video {
  return {
    id: 'v1', title: 'clip', status: 'processing',
    duration: null, width: null, height: null, size: null,
    master_playlist: null, thumbnail: null,
    created_at: '', updated_at: '',
    ...overrides,
  }
}

beforeEach(() => {
  vi.mocked(api.createVideo).mockResolvedValue({
    video: video({ status: 'uploading' }),
    upload: { uploadUrl: 'http://s3.test/raw/v1', method: 'PUT', expiresAt: '', headers: {} },
  })
  vi.mocked(api.uploadFile).mockResolvedValue(undefined)
  vi.mocked(api.completeUpload).mockResolvedValue(video({ status: 'processing' }))
})
afterEach(() => vi.clearAllMocks())

async function selectFile() {
  const file = new File(['bytes'], 'clip.mp4', { type: 'video/mp4' })
  await userEvent.upload(screen.getByLabelText(/video file/i), file)
  await userEvent.click(screen.getByRole('button', { name: /upload/i }))
}

describe('App', () => {
  it('uploads, then polls until the video is ready and shows the player', async () => {
    vi.mocked(api.getVideo)
      .mockResolvedValueOnce(video({ status: 'processing' }))
      .mockResolvedValue(video({ status: 'ready', master_playlist: 'http://cdn.test/processed/v1/master.m3u8' }))

    render(<App pollMs={10} />)
    await selectFile()

    await waitFor(() => expect(api.createVideo).toHaveBeenCalledWith('clip.mp4'))
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalled())
    await waitFor(() => expect(api.completeUpload).toHaveBeenCalledWith('v1'))

    await waitFor(() => expect(screen.getByTestId('player')).toBeInTheDocument())
    expect(screen.getByTestId('player')).toHaveAttribute('data-src', 'http://cdn.test/processed/v1/master.m3u8')
  })

  it('still reaches ready when the optional complete call fails', async () => {
    vi.mocked(api.completeUpload).mockRejectedValue(new Error('409: invalid_state_transition'))
    vi.mocked(api.getVideo)
      .mockResolvedValueOnce(video({ status: 'processing' }))
      .mockResolvedValue(video({ status: 'ready', master_playlist: 'http://cdn.test/processed/v1/master.m3u8' }))

    render(<App pollMs={10} />)
    await selectFile()

    await waitFor(() => expect(api.completeUpload).toHaveBeenCalledWith('v1'))

    await waitFor(() => expect(screen.getByTestId('player')).toBeInTheDocument())
    expect(screen.getByTestId('player')).toHaveAttribute('data-src', 'http://cdn.test/processed/v1/master.m3u8')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('clears a transient poll error once a later poll succeeds', async () => {
    vi.mocked(api.getVideo)
      .mockRejectedValueOnce(new Error('network blip'))
      .mockResolvedValue(video({ status: 'ready', master_playlist: 'http://cdn.test/processed/v1/master.m3u8' }))

    render(<App pollMs={10} />)
    await selectFile()

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/network blip/i))

    await waitFor(() => expect(screen.getByTestId('player')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows the error state when processing fails', async () => {
    vi.mocked(api.getVideo).mockResolvedValue(video({ status: 'failed' }))

    render(<App pollMs={10} />)
    await selectFile()

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/failed/i))
    expect(screen.queryByTestId('player')).not.toBeInTheDocument()
  })
})
