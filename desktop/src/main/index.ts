import { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, shell, Tray } from 'electron';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { Sidecar, type Endpoint } from './sidecar';

const isWin = process.platform === 'win32';
let win: BrowserWindow | null = null;
let tray: Tray | null = null;
let quitting = false;
const sidecar = new Sidecar(enginePath());
const pendingUrls: string[] = [];

function enginePath(): string {
  if (process.env.VIDHAWK_ENGINE) return process.env.VIDHAWK_ENGINE;
  const name = isWin ? 'vidhawk.exe' : 'vidhawk';
  if (app.isPackaged) return join(process.resourcesPath, 'bin', name);
  const candidates = [join(app.getAppPath(), 'resources', 'bin', name), join(app.getAppPath(), '..', 'engine', 'dist', name)];
  return candidates.find((p) => existsSync(p)) ?? candidates[0];
}

function assetPath(...parts: string[]): string {
  return app.isPackaged ? join(process.resourcesPath, ...parts) : join(app.getAppPath(), 'resources', ...parts);
}

function extractUrls(argv: string[]): string[] {
  return argv.filter((a) => /^magnet:/i.test(a) || /\.torrent$/i.test(a) || /^https?:\/\//i.test(a));
}

function deliverUrl(url: string): void {
  if (win && !win.isDestroyed()) {
    showWindow();
    win.webContents.send('add-url', url);
  } else {
    pendingUrls.push(url);
  }
}

function showWindow(): void {
  if (!win) return;
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

function createWindow(): void {
  win = new BrowserWindow({
    width: 1120,
    height: 740,
    minWidth: 820,
    minHeight: 560,
    show: false,
    backgroundColor: '#0f172a',
    autoHideMenuBar: true,
    title: 'VidHawk',
    icon: isWin ? join(app.getAppPath(), 'build', 'icon.ico') : undefined,
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  });
  win.once('ready-to-show', () => {
    showWindow();
    for (const u of pendingUrls.splice(0)) win?.webContents.send('add-url', u);
  });
  win.on('close', (e) => {
    if (!quitting) {
      e.preventDefault();
      win?.hide();
    }
  });
  win.on('closed', () => { win = null; });
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:/i.test(url)) shell.openExternal(url);
    return { action: 'deny' };
  });
  if (process.env.ELECTRON_RENDERER_URL) win.loadURL(process.env.ELECTRON_RENDERER_URL);
  else win.loadFile(join(__dirname, '../renderer/index.html'));
}

function createTray(): void {
  const icon = nativeImage.createFromPath(assetPath('tray', isWin ? 'tray.ico' : 'tray.png'));
  tray = new Tray(icon.isEmpty() ? nativeImage.createEmpty() : icon);
  tray.setToolTip('VidHawk');
  const rebuild = () => {
    tray?.setContextMenu(Menu.buildFromTemplate([
      { label: 'Open VidHawk', click: () => (win ? showWindow() : createWindow()) },
      { label: 'Add download…', click: () => { win ? showWindow() : createWindow(); win?.webContents.send('add-url', ''); } },
      { type: 'separator' },
      { label: 'Start with system', type: 'checkbox', checked: app.getLoginItemSettings().openAtLogin, click: (item) => { app.setLoginItemSettings({ openAtLogin: item.checked }); rebuild(); } },
      { type: 'separator' },
      { label: 'Quit', click: () => { quitting = true; app.quit(); } },
    ]));
  };
  rebuild();
  tray.on('click', () => (win ? showWindow() : createWindow()));
}

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on('second-instance', (_e, argv) => {
    showWindow();
    extractUrls(argv).forEach(deliverUrl);
  });
  app.on('open-url', (e, url) => { e.preventDefault(); deliverUrl(url); });
  app.on('open-file', (e, path) => { e.preventDefault(); deliverUrl(path); });

  app.whenReady().then(async () => {
    if (app.isPackaged) {
      app.setAsDefaultProtocolClient('magnet');
    }
    ipcMain.handle('engine:endpoint', () => sidecar.endpoint);
    ipcMain.handle('engine:path', () => sidecar.exe);
    ipcMain.handle('shell:showItem', (_e, p: string) => shell.showItemInFolder(p));
    ipcMain.handle('shell:openExternal', (_e, u: string) => (/^https?:/i.test(u) ? shell.openExternal(u) : undefined));
    ipcMain.handle('dialog:pickDir', async (_e, current?: string) => {
      const r = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'], defaultPath: current });
      return r.canceled ? null : r.filePaths[0];
    });
    ipcMain.handle('app:version', () => app.getVersion());
    ipcMain.handle('app:loginItem:get', () => app.getLoginItemSettings().openAtLogin);
    ipcMain.handle('app:loginItem:set', (_e, v: boolean) => app.setLoginItemSettings({ openAtLogin: v }));

    sidecar.onEndpoint((ep: Endpoint | null) => win?.webContents.send('engine:endpoint', ep));
    sidecar.start();
    createWindow();
    createTray();
    extractUrls(process.argv.slice(1)).forEach(deliverUrl);
  });

  app.on('before-quit', () => { quitting = true; });
  app.on('will-quit', () => sidecar.stop());
  app.on('window-all-closed', () => { /* keep running in the tray */ });
  app.on('activate', () => { if (!win) createWindow(); else showWindow(); });
}
