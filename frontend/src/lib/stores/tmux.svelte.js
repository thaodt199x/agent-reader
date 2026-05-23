import { writable } from 'svelte/store';

export const tmuxSessionPickerOpen = writable(false);
export const tmuxTerminalTarget = writable(null); // session name or null
