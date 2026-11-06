import type { Download } from './types'

declare global {
  interface Window {
    go: { main: { App: {
      ListDownloads(): Promise<Download[]>
      AddDownload(url: string, destination: string, filename: string): Promise<Download>
      PauseDownload(id: string): Promise<void>
      ResumeDownload(id: string): Promise<void>
      CancelDownload(id: string): Promise<void>
      SelectDirectory(): Promise<string>
    } } }
    runtime?: { EventsOn(event: string, callback: (download: Download) => void): () => void }
  }
}
export {}
