export type Status = 'QUEUED' | 'INITIALIZING' | 'DOWNLOADING' | 'PAUSING' | 'PAUSED' | 'VERIFYING' | 'COMPLETED' | 'FAILED' | 'CANCELLED'
export interface Download {
  id: string; url: string; finalUrl: string; filename: string; destination: string; mimeType: string
  size: number; downloaded: number; rangeSupported: boolean; connections: number; retries: number
  status: Status; error: string; createdAt: string; startedAt: string; completedAt: string
}
