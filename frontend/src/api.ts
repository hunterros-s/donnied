export interface AppState {
  process: ProcessState;
  device: DeviceState;
  sleep: SleepState;
  history: HistoryState;
}

export interface ProcessState {
  pid: number;
  start_time: string;
  uptime: string;
}

export interface DeviceState {
  state: string;
  connected: boolean;
  paired_addr: string;
  paired_name: string;
  last_seen?: string;
  battery?: BatteryInfo;
  heart_rate?: number;
  spo2?: number;
  steps?: number;
  calories?: number;
  distance?: number;
  live_activity?: LiveActivity;
}

export interface BatteryInfo {
  level: number;
  charging: boolean;
}

export interface LiveActivity {
  steps: number;
  calories: number;
  distance: number;
}

export interface SleepState {
  state: string;
  stage?: string;
  stage_raw?: number;
  since?: string;
  last_sync?: string;
  source?: string;
  stale: boolean;
  error?: string;
}

export interface HistoryState {
  selected_day: string;
  from?: string;
  to?: string;
  sleep_sessions?: SleepSession[];
  device_sleep?: DeviceSleep[];
  hr_samples?: HRSample[];
  step_details?: StepDetail[];
  spo2_days?: SpO2Day[];
  errors?: Record<string, string>;
}

export interface SleepSession {
  id: string;
  device_addr: string;
  start: string;
  end: string;
  total_minutes: number;
  source: string;
  first_seen: string;
  last_seen: string;
  segments: SleepSegment[];
}

export interface SleepSegment {
  index: number;
  start: string;
  end: string;
  minutes: number;
  stage_raw: number;
  stage: string;
  state: string;
  source: string;
}

export interface DeviceSleep {
  start: string;
  end: string;
  stages: Array<{ stage: number; minutes: number }>;
}

export interface HRSample {
  time: string;
  bpm: number;
  interval: number;
}

export interface StepDetail {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  steps: number;
  calories: number;
  distance: number;
}

export interface SpO2Day {
  days_ago: number;
  samples: Array<{ min: number; max: number }>;
}

export interface ScanResult {
  address: string;
  name: string;
  rssi: number;
  has_uart: boolean;
}

export interface RealtimeReading {
  kind: number;
  value: number;
}

export interface ActionResult {
  message?: string;
  state?: AppState;
  scan_results?: ScanResult[];
  realtime_reading?: RealtimeReading;
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const message = (await res.text()).trim() || `${res.status} ${res.statusText}`;
    throw new Error(message);
  }
  return res.json() as Promise<T>;
}

export async function getState(signal?: AbortSignal): Promise<AppState> {
  return parseResponse<AppState>(await fetch('/api/state?history=true', { signal }));
}

export async function postAction(action: string, body?: unknown): Promise<ActionResult> {
  return parseResponse<ActionResult>(
    await fetch(`/api/actions/${action}`, {
      method: 'POST',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    }),
  );
}
