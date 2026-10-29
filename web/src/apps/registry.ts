import { defineAsyncComponent, type Component } from 'vue'
import type { AppManifest } from './types'
import { viewableImageTypes } from './image-viewer/viewer'

// What the worker calls text: text/* and the common source and config
// types that are not under text/ (mime.types, Go's own table).
export const textMimeTypes = [
  'text/*',
  'application/json',
  'application/xml',
  'application/javascript',
  'application/x-javascript',
  'application/x-sh',
  'application/x-shellscript',
  'application/x-perl',
  'application/x-ruby',
  'application/x-php',
  'application/x-python',
  'application/yaml',
  'application/x-yaml',
  'application/toml',
  'application/sql',
  'application/x-desktop',
  'application/x-zerosize',
  // Playlists are text; listed so they do not match the media player's audio/*.
  'audio/x-mpegurl',
  'audio/mpegurl',
  'audio/x-scpls',
]

export const apps: AppManifest[] = [
  {
    id: 'files',
    name: 'Files',
    comment: 'Access and organize files',
    icon: 'system-file-manager',
    category: 'Accessories',
    mimeTypes: ['inode/directory'],
    defaultSize: { w: 920, h: 580 },
    minSize: { w: 420, h: 280 },
    component: () => import('./files/FilesApp.vue'),
  },
  {
    id: 'terminal',
    name: 'Terminal',
    comment: 'Use the command line',
    icon: 'utilities-terminal',
    category: 'System Tools',
    defaultSize: { w: 760, h: 470 },
    minSize: { w: 320, h: 180 },
    component: () => import('./terminal/TerminalApp.vue'),
  },
  {
    id: 'text-editor',
    name: 'Text Editor',
    comment: 'Edit text files',
    icon: 'accessories-text-editor',
    category: 'Accessories',
    mimeTypes: textMimeTypes,
    reuseWindow: true,
    defaultSize: { w: 760, h: 540 },
    minSize: { w: 360, h: 240 },
    component: () => import('./editor/EditorApp.vue'),
  },
  {
    id: 'image-viewer',
    name: 'Image Viewer',
    comment: 'Browse and rotate images',
    icon: 'xviewer',
    category: 'Graphics',
    mimeTypes: viewableImageTypes,
    defaultSize: { w: 820, h: 600 },
    minSize: { w: 360, h: 260 },
    component: () => import('./image-viewer/ImageViewerApp.vue'),
  },
  {
    id: 'media-player',
    name: 'Media Player',
    comment: 'Play music and videos',
    icon: 'multimedia-video-player',
    category: 'Sound & Video',
    mimeTypes: ['audio/*', 'video/*'],
    defaultSize: { w: 800, h: 520 },
    minSize: { w: 420, h: 220 },
    component: () => import('./media-player/MediaPlayerApp.vue'),
  },
  {
    id: 'file-properties',
    name: 'Properties',
    comment: 'Show and change the properties of files and folders',
    icon: 'system-file-manager',
    category: 'Accessories',
    // Launched by Files and the desktop with { paths }.
    hidden: true,
    defaultSize: { w: 460, h: 440 },
    minSize: { w: 360, h: 320 },
    component: () => import('./properties/PropertiesApp.vue'),
  },
  {
    id: 'file-chooser',
    name: 'Choose a File',
    comment: 'Open or save files for other apps',
    icon: 'folder-open',
    category: 'Accessories',
    // Opened by ctx.chooseFile as a dialog of the app that asks (D49).
    hidden: true,
    defaultSize: { w: 780, h: 520 },
    minSize: { w: 480, h: 320 },
    component: () => import('./chooser/FileChooserApp.vue'),
  },
  {
    id: 'backgrounds',
    name: 'Backgrounds',
    comment: 'Change your desktop background',
    icon: 'preferences-desktop-wallpaper',
    category: 'Preferences',
    singleInstance: true,
    defaultSize: { w: 900, h: 580 },
    minSize: { w: 480, h: 340 },
    component: () => import('./backgrounds/BackgroundsApp.vue'),
  },
  {
    id: 'settings',
    name: 'System Settings',
    comment: 'Control Center: screen lock, effects, panel and more',
    icon: 'preferences-system',
    category: 'Preferences',
    singleInstance: true,
    defaultSize: { w: 720, h: 500 },
    minSize: { w: 520, h: 340 },
    component: () => import('./settings/SettingsApp.vue'),
  },
  {
    id: 'web-apps',
    name: 'Web Apps',
    comment: 'Open the web apps your programs serve (Jupyter, code-server) in a window',
    icon: 'applications-internet',
    category: 'Internet',
    singleInstance: true,
    defaultSize: { w: 560, h: 420 },
    minSize: { w: 420, h: 260 },
    component: () => import('./web-apps/WebAppsApp.vue'),
  },
  {
    id: 'web-view',
    name: 'Web App',
    comment: 'A local web app in a window',
    icon: 'internet-web-browser',
    category: 'Internet',
    // Launched by Web Apps with { port }.
    hidden: true,
    defaultSize: { w: 1000, h: 700 },
    minSize: { w: 360, h: 240 },
    component: () => import('./web-apps/WebViewApp.vue'),
  },
  {
    id: 'system-monitor',
    name: 'System Monitor',
    comment: 'View current processes and monitor system state',
    icon: 'utilities-system-monitor',
    category: 'Administration',
    singleInstance: true,
    defaultSize: { w: 780, h: 540 },
    minSize: { w: 480, h: 300 },
    component: () => import('./system-monitor/SystemMonitorApp.vue'),
  },
  {
    id: 'system-info',
    name: 'System Info',
    comment: 'View information about this computer',
    icon: 'utilities-system-monitor',
    category: 'Administration',
    singleInstance: true,
    defaultSize: { w: 560, h: 440 },
    minSize: { w: 380, h: 300 },
    component: () => import('./system-info/SystemInfoApp.vue'),
  },
]

const byId = new Map(apps.map((a) => [a.id, a]))

export function getApp(id: string): AppManifest | undefined {
  return byId.get(id)
}

/** The app that opens mime, by exact match first, then "type/*". */
/** Types the text editor opens: its own list, `text/*` among them. */
export function isTextMime(mime: string): boolean {
  return textMimeTypes.includes(mime) || (mime.startsWith('text/') && textMimeTypes.includes('text/*'))
}

export function appForMime(mime: string): AppManifest | undefined {
  const major = mime.split('/')[0]
  return apps.find((a) => a.mimeTypes?.includes(mime)) ?? apps.find((a) => a.mimeTypes?.includes(`${major}/*`))
}

const components = new Map<string, Component>()

/** One async component per app, created on first use. */
export function appComponent(id: string): Component | undefined {
  const app = byId.get(id)
  if (!app) return undefined
  let c = components.get(id)
  if (!c) {
    c = defineAsyncComponent(app.component)
    components.set(id, c)
  }
  return c
}
