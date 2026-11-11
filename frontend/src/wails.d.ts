import type {
  AddRequest, AppInfo, BrowserSetup, CleanResult, DiskInfo, Download, HelperInfo, LinkInfo, MediaRow, NetworkSettings, Options, Queue,
  Schedule, Segment, ServerLocation, Settings, SiteLogin, SpeedLimit, VideoChoice, VideoLookUp,
} from './types'

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

  // Queues and their schedules.
  ListQueues(): Promise<Queue[]>
  CreateQueue(name: string): Promise<Queue>
  DeleteQueue(id: string): Promise<void>
  AddToQueue(queueId: string, downloadIds: string[]): Promise<void>
  RemoveFromQueue(downloadId: string): Promise<void>
  MoveInQueue(downloadId: string, delta: number): Promise<void>
  StartQueue(id: string): Promise<void>
  StopQueue(id: string): Promise<void>
  SetQueueFilesAtOnce(id: string, n: number): Promise<void>
  SetQueueSchedule(id: string, s: Schedule): Promise<void>
  GetNetworkSettings(): Promise<NetworkSettings>
  SaveNetworkSettings(s: NetworkSettings): Promise<NetworkSettings>

  // The other dialogs.
  FileChecksum(id: string, algorithm: string): Promise<string>
  DiskSpace(dir: string): Promise<DiskInfo>
  CleanDisk(): Promise<CleanResult>
  GetSpeedLimit(): Promise<SpeedLimit>
  SetSpeedLimit(s: SpeedLimit): Promise<SpeedLimit>
  ListSiteLogins(): Promise<SiteLogin[]>
  SaveSiteLogin(site: string, user: string, password: string): Promise<SiteLogin[]>
  RemoveSiteLogin(site: string): Promise<SiteLogin[]>
  GetOptions(): Promise<Options>
  SaveOptions(o: Options): Promise<Options>
  GetBrowserSetup(): Promise<BrowserSetup>
  /** Opens the browser where the extension is added: "store" or "unpacked" (folder copied to the clipboard). */
  AddExtensionTo(name: string): Promise<'store' | 'unpacked'>
  FinishBrowserSetup(): Promise<void>
  ServerLocation(id: string): Promise<ServerLocation>
  FlagsPicture(): Promise<string>
  ExtractDownload(id: string, password: string): Promise<void>

  // Videos (yt-dlp).
  GetHelpers(): Promise<HelperInfo[]>
  InstallHelper(name: string): Promise<HelperInfo>
  LookUpVideo(pageUrl: string, language: string): Promise<VideoLookUp>
  StartVideoDownload(req: {
    url: string; title: string; choice: VideoChoice; directory: string
    subs: { langs: string[]; automatic: boolean; embed: boolean }
  }): Promise<MediaRow>
  ListMediaDownloads(): Promise<MediaRow[]>
  StopMediaDownload(id: string): Promise<void>
}

declare global {
  interface Window {
    go?: { main: { App: AppBindings } }
    runtime?: {
      EventsOn(event: string, callback: (...data: any[]) => void): () => void
      ClipboardSetText?(text: string): Promise<boolean>
      BrowserOpenURL?(url: string): void
      WindowMinimise?(): void
      WindowToggleMaximise?(): void
      WindowSetMinSize?(width: number, height: number): void
      WindowSetSize?(width: number, height: number): void
      Quit?(): void
    }
  }
}

export {}
