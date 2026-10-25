import type { VidHawkBridge } from '../../preload/index';

declare global {
  interface Window {
    vidhawk: VidHawkBridge;
  }
}

export {};
