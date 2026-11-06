import { Archive, AppWindow, FileText, Film, Image, Music, File, Layers } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

export type Category = 'all' | 'video' | 'music' | 'archives' | 'programs' | 'documents' | 'images' | 'other'
type FileCategory = Exclude<Category, 'all'>

const EXTENSIONS: Record<Exclude<FileCategory, 'other'>, string[]> = {
  video: ['mp4', 'mkv', 'webm', 'avi', 'mov', 'wmv', 'flv', 'm4v', '3gp', 'ts', 'mpg', 'mpeg'],
  music: ['mp3', 'm4a', 'aac', 'flac', 'wav', 'ogg', 'opus', 'wma', 'weba'],
  archives: ['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'iso', 'zst'],
  programs: ['exe', 'msi', 'apk', 'deb', 'rpm', 'appimage', 'dmg', 'pkg', 'jar', 'bat', 'sh'],
  documents: ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'txt', 'epub', 'csv', 'odt', 'rtf', 'md', 'json', 'xml', 'html'],
  images: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'tif', 'tiff', 'heic', 'avif', 'ico'],
}

export const CATEGORIES: { id: Category; label: string; icon: LucideIcon }[] = [
  { id: 'all', label: 'All downloads', icon: Layers },
  { id: 'video', label: 'Video', icon: Film },
  { id: 'music', label: 'Music', icon: Music },
  { id: 'archives', label: 'Archives', icon: Archive },
  { id: 'programs', label: 'Programs', icon: AppWindow },
  { id: 'documents', label: 'Documents', icon: FileText },
  { id: 'images', label: 'Images', icon: Image },
  { id: 'other', label: 'Other', icon: File },
]

export function categoryOf(filename: string): FileCategory {
  const dot = filename.lastIndexOf('.')
  if (dot < 0) return 'other'
  const ext = filename.slice(dot + 1).toLowerCase()
  for (const [cat, exts] of Object.entries(EXTENSIONS)) {
    if (exts.includes(ext)) return cat as FileCategory
  }
  return 'other'
}

export function iconFor(filename: string): LucideIcon {
  const cat = categoryOf(filename)
  return cat === 'other' ? File : CATEGORIES.find((c) => c.id === cat)?.icon ?? File
}
