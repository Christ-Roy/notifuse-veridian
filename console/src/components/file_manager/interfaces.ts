export interface FileInfo {
  size: number
  size_human: string
  content_type: string // guessed from the file extension
  url: string
}

export interface StorageObject {
  key: string
  name: string
  is_folder: boolean
  path: string
  last_modified: Date
  file_info: FileInfo
}

export interface FileManagerProps {
  currentPath?: string
  itemFilters?: ItemFilter[]
  onError: (error: Error) => void
  onSelect: (items: StorageObject[]) => void
  height: number
  acceptFileType: string
  acceptItem: (item: StorageObject) => boolean
  withSelection?: boolean
  multiple?: boolean
  settings?: FileManagerSettings
  onUpdateSettings: (settings: FileManagerSettings) => Promise<void>
  settingsInfo?: React.ReactNode
  readOnly?: boolean
  // Controlled path mode for router sync
  controlledPath?: string
  onPathChange?: (path: string) => void
}

export interface ItemFilter {
  key: string // item key
  value: string | number | boolean
  operator: string // contains equals greaterThan lessThan
}

export interface FileManagerSettings {
  provider?: string
  endpoint: string
  access_key: string
  bucket: string
  region?: string
  // Write-only. Existing values are represented by has_secret_key.
  secret_key?: string
  has_secret_key?: boolean
  cdn_endpoint?: string
  force_path_style?: boolean
}
