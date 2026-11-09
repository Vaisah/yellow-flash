import type { AddRequest, AppInfo, Download, LinkInfo, Segment, Settings } from './types'

/** Bindings injected by the Wails runtime for the Go App struct. */
export interface AppBindings {
  Info(): Promise<AppInfo>
  ListDownloads(): Promise<Download[]>
  GetDownload(id: string): Promise<Download>
  InspectLink(url: string): Promise<LinkInfo>
  AddDownload(req: AddRequest): Promise<Download>
  ResumeDownload(id: string): Promise<void>
  PauseDownload(id: string): Promise<void>
  CancelDownload(id: string): Promise<void>
  RetryDownload(id: string): Promise<void>
  DeleteDownload(id: string, deleteFile: boolean): Promise<void>
  PauseAll(): Promise<void>
  ResumeAll(): Promise<void>
  GetSegments(id: string): Promise<Segment[]>
  OpenFile(id: string): Promise<void>
  ShowInFolder(id: string): Promise<void>
  OpenFolder(dir: string): Promise<void>
  SelectDirectory(): Promise<string>
  ClipboardURL(): Promise<string>
  GetSettings(): Promise<Settings>
  SaveSettings(s: Settings): Promise<Settings>
}

declare global {
  interface Window {
    go?: { main: { App: AppBindings } }
    runtime?: {
      EventsOn(event: string, callback: (...data: any[]) => void): () => void
      ClipboardSetText?(text: string): Promise<boolean>
      WindowMinimise?(): void
      WindowToggleMaximise?(): void
      WindowSetMinSize?(width: number, height: number): void
      WindowSetSize?(width: number, height: number): void
      Quit?(): void
    }
  }
}

export {}
