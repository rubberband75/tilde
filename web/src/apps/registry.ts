import { defineAsyncComponent, type Component } from 'vue'
import type { AppManifest } from './types'

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
    // What the worker calls text: text/* and the common source and
    // config types that are not under text/ (mime.types, Go's own table).
    mimeTypes: [
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
    ],
    defaultSize: { w: 760, h: 540 },
    minSize: { w: 360, h: 240 },
    component: () => import('./editor/EditorApp.vue'),
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
