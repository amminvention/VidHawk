import { contextBridge, ipcRenderer, webUtils } from 'electron';

export interface Endpoint {
  port: number;
  token: string;
  pid: number;
}

const bridge = {
  endpoint: (): Promise<Endpoint | null> => ipcRenderer.invoke('engine:endpoint'),
  enginePath: (): Promise<string> => ipcRenderer.invoke('engine:path'),
  onEndpoint: (cb: (ep: Endpoint | null) => void): (() => void) => {
    const h = (_e: unknown, ep: Endpoint | null) => cb(ep);
    ipcRenderer.on('engine:endpoint', h);
    return () => ipcRenderer.off('engine:endpoint', h);
  },
  onAddUrl: (cb: (url: string) => void): (() => void) => {
    const h = (_e: unknown, url: string) => cb(url);
    ipcRenderer.on('add-url', h);
    return () => ipcRenderer.off('add-url', h);
  },
  showItem: (p: string): Promise<void> => ipcRenderer.invoke('shell:showItem', p),
  openExternal: (u: string): Promise<void> => ipcRenderer.invoke('shell:openExternal', u),
  pickDirectory: (current?: string): Promise<string | null> => ipcRenderer.invoke('dialog:pickDir', current),
  version: (): Promise<string> => ipcRenderer.invoke('app:version'),
  loginItem: {
    get: (): Promise<boolean> => ipcRenderer.invoke('app:loginItem:get'),
    set: (v: boolean): Promise<void> => ipcRenderer.invoke('app:loginItem:set', v),
  },
  pathForFile: (f: File): string => webUtils.getPathForFile(f),
};

export type VidHawkBridge = typeof bridge;

contextBridge.exposeInMainWorld('vidhawk', bridge);
