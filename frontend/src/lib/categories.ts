import { Archive, Bot, File, FileText, Film, Image, Laptop, Layers, Music } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

export type Category = 'all' | 'music' | 'compressed' | 'archives' | 'video' | 'programs' | 'documents' | 'apks' | 'images' | 'other'

export interface CategoryInfo {
  id: Category
  label: string
  singularLabel: string
  icon: LucideIcon
}

const EXTENSIONS: Record<string, string[]> = {
  music: ['mp3', 'm4a', 'aac', 'flac', 'wav', 'ogg', 'opus', 'wma', 'weba'],
  compressed: ['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'iso', 'zst', 'pkg'],
  video: ['mp4', 'mkv', 'webm', 'avi', 'mov', 'wmv', 'flv', 'm4v', '3gp', 'ts', 'mpg', 'mpeg'],
  programs: ['exe', 'msi', 'deb', 'rpm', 'appimage', 'dmg', 'jar', 'bat', 'sh', 'bin'],
  documents: ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'txt', 'epub', 'csv', 'odt', 'rtf', 'md', 'json', 'xml', 'html'],
  apks: ['apk', 'xapk', 'apkm'],
  images: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'tif', 'tiff', 'heic', 'avif', 'ico'],
}

export const CATEGORIES: CategoryInfo[] = [
  { id: 'all', label: 'All Downloads', singularLabel: 'File', icon: Layers },
  { id: 'music', label: 'Musics', singularLabel: 'Music', icon: Music },
  { id: 'compressed', label: 'Compressed', singularLabel: 'Archive', icon: Archive },
  { id: 'video', label: 'Videos', singularLabel: 'Video', icon: Film },
  { id: 'programs', label: 'Programs', singularLabel: 'Program', icon: Laptop },
  { id: 'documents', label: 'Documents', singularLabel: 'Document', icon: FileText },
  { id: 'apks', label: 'APKs', singularLabel: 'App', icon: Bot },
  { id: 'images', label: 'Images', singularLabel: 'Image', icon: Image },
]

export function categoryOf(filename: string): Category {
  const dot = filename.lastIndexOf('.')
  if (dot < 0) return 'other'
  const ext = filename.slice(dot + 1).toLowerCase()
  for (const [cat, exts] of Object.entries(EXTENSIONS)) {
    if (exts.includes(ext)) return cat as Category
  }
  return 'other'
}

export function categoryLabel(filename: string): string {
  const cat = categoryOf(filename)
  const match = CATEGORIES.find((c) => c.id === cat)
  if (match) return match.singularLabel
  if (cat === 'archives' || cat === 'compressed') return 'Archive'
  if (cat === 'apks') return 'App'
  return 'File'
}

export function iconFor(filename: string): LucideIcon {
  const cat = categoryOf(filename)
  const match = CATEGORIES.find((c) => c.id === cat)
  if (match) return match.icon
  if (cat === 'archives' || cat === 'compressed') return Archive
  return File
}
