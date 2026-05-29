import { render } from 'preact';
import type { ComponentChildren } from 'preact';
import { useCallback, useEffect, useRef, useState } from 'preact/hooks';
import * as echarts from 'echarts/core';
import { BarChart, CustomChart, LineChart } from 'echarts/charts';
import { GridComponent, TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import { Activity, Battery, Bluetooth, Clock, HeartPulse, Moon, Radar, RefreshCw, Search } from 'lucide-preact';
import './styles.css';
import type { ActionResult, AppState, HRSample, ScanResult, SleepSession, StepDetail } from './api';
import { getState, postAction } from './api';

echarts.use([BarChart, CustomChart, GridComponent, LineChart, TooltipComponent, CanvasRenderer]);

type BusyAction = string | null;
type Tone = 'default' | 'primary' | 'danger';
type EChartsOption = Record<string, unknown>;
type RealtimeValues = Partial<Record<number, number>>;

const REALTIME = { heartRate: 1, spo2: 3 } as const;
const SLEEP_WINDOW_START_HOUR = 18;
const SLEEP_WINDOW_HOURS = 18;

const sleepPalette: Record<string, string> = {
  awake: '#f59e0b',
  rem: '#38bdf8',
  core: '#2563eb',
  deep: '#3730a3',
  unknown: '#94a3b8',
};
const sleepStageLabels: Record<string, string> = {
  awake: 'Awake',
  rem: 'REM',
  core: 'Core',
  deep: 'Deep',
  unknown: 'Unknown',
};

function App() {
  const [state, setState] = useState<AppState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<BusyAction>(null);
  const [scanResults, setScanResults] = useState<ScanResult[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [realtime, setRealtime] = useState<RealtimeValues>({});

  const loadState = useCallback(async (signal?: AbortSignal) => {
    try {
      const next = await getState(signal);
      setState(next);
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      if ((err as Error).name !== 'AbortError') setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadState(controller.signal);
    const timer = window.setInterval(() => void loadState(), 5000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [loadState]);

  const applyAction = useCallback((result: ActionResult) => {
    if (result.state) {
      setState(result.state);
      setLastUpdated(new Date());
    }
    if (result.message) {
      setNotice(result.message);
      window.setTimeout(() => setNotice(null), 2200);
    }
    if (result.realtime_reading) {
      setRealtime((prev) => ({ ...prev, [result.realtime_reading!.kind]: result.realtime_reading!.value }));
    }
  }, []);

  const runAction = useCallback(
    async (label: string, action: () => Promise<ActionResult>) => {
      setBusy(label);
      setError(null);
      try {
        const result = await action();
        applyAction(result);
        return result;
      } catch (err) {
        setError((err as Error).message);
        throw err;
      } finally {
        setBusy(null);
      }
    },
    [applyAction],
  );

  const scan = useCallback(async () => {
    const result = await runAction('Scanning', () => postAction('scan'));
    setScanResults(result.scan_results ?? []);
  }, [runAction]);

  const pair = useCallback(async (device: ScanResult) => {
    await runAction('Pairing', () => postAction('pair', { addr: device.address, name: device.name }));
  }, [runAction]);

  return (
    <main className="min-h-screen bg-zinc-100 text-zinc-950">
      <TopBar state={state} loading={loading} lastUpdated={lastUpdated} onRefresh={() => void loadState()} />
      <div className="mx-auto w-full max-w-screen-2xl px-3 py-3 sm:px-5 lg:px-6">
        {(error || notice) && (
          <div className="mb-3 grid gap-2">
            {error && <Alert tone="error" title="Error" body={error} />}
            {notice && <Alert tone="success" title="Done" body={notice} />}
          </div>
        )}

        {state ? (
          <Dashboard state={state} realtime={realtime} busy={busy} scanResults={scanResults} onScan={scan} onPair={pair} onAction={runAction} />
        ) : (
          <Panel className="grid min-h-72 place-items-center text-center">
            <div>
              <RefreshCw className="mx-auto mb-3 animate-spin text-zinc-400" size={22} />
              <h1 className="text-base font-semibold">Connecting to donnied</h1>
              <p className="mt-1 text-sm text-zinc-500">Start the daemon and refresh if this does not load.</p>
            </div>
          </Panel>
        )}
      </div>
    </main>
  );
}

function Dashboard({ state, realtime, busy, scanResults, onScan, onPair, onAction }: { state: AppState; realtime: RealtimeValues; busy: BusyAction; scanResults: ScanResult[]; onScan: () => Promise<void>; onPair: (device: ScanResult) => Promise<void>; onAction: (label: string, action: () => Promise<ActionResult>) => Promise<ActionResult> }) {
  const metrics = displayMetrics(state, realtime);
  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_360px] xl:grid-cols-[minmax(0,1fr)_400px]">
      <div className="grid min-w-0 gap-3">
        <DeviceHero state={state} busy={busy} onScan={onScan} onAction={onAction} />

        <section className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-5">
          <Metric icon={<Battery size={15} />} label="Battery" value={metrics.battery.value} detail={metrics.battery.detail} />
          <Metric icon={<HeartPulse size={15} />} label="Heart rate" value={metrics.hr.value} unit={metrics.hr.unit} detail={metrics.hr.detail} />
          <Metric icon={<Activity size={15} />} label="SpO₂" value={metrics.spo2.value} detail={metrics.spo2.detail} />
          <Metric icon={<Activity size={15} />} label="Steps" value={metrics.steps.value} detail={metrics.steps.detail} />
          <Metric icon={<Moon size={15} />} label="Last sleep" value={metrics.sleep.value} detail={metrics.sleep.detail} className="col-span-2 sm:col-span-1" />
        </section>

        <ChartPanel title="Heart rate" subtitle="Recorded samples">
          <ChartEmpty hasData={(state.history.hr_samples?.length ?? 0) > 1} label="No heart-rate history yet. Use Sync all or Read HR.">
            <TimeSeriesChart option={heartRateOption(state.history.hr_samples ?? [])} />
          </ChartEmpty>
        </ChartPanel>

        <ChartPanel title="Sleep" subtitle="Nightly stages">
          <SleepSummary state={state} />
          <ChartEmpty hasData={sleepSegments(state.history.sleep_sessions ?? []).length > 0} label="No sleep-stage records yet.">
            <TimeSeriesChart option={sleepStageOption(state.history.sleep_sessions ?? [])} className="min-h-[300px]" />
          </ChartEmpty>
        </ChartPanel>

        <ChartPanel title="Steps" subtitle="Hourly buckets">
          <ChartEmpty hasData={(state.history.step_details ?? []).some((d) => d.steps > 0)} label="No step history yet. Use Sync all.">
            <TimeSeriesChart option={stepsOption(state.history.step_details ?? [])} />
          </ChartEmpty>
        </ChartPanel>
      </div>

      <aside className="grid content-start gap-3 lg:sticky lg:top-16">
        <DeviceActions state={state} busy={busy} onAction={onAction} />
        <ScanPanel results={scanResults} busy={busy} pairedAddr={state.device.paired_addr} onScan={onScan} onPair={onPair} />
        <DataPanel state={state} />
      </aside>
    </div>
  );
}

function TopBar({ state, loading, lastUpdated, onRefresh }: { state: AppState | null; loading: boolean; lastUpdated: Date | null; onRefresh: () => void }) {
  return (
    <header className="sticky top-0 z-20 border-b border-zinc-200 bg-white/95 backdrop-blur">
      <div className="mx-auto flex min-h-12 max-w-screen-2xl items-center justify-between gap-3 px-3 py-2 sm:px-5 lg:px-6">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold tracking-tight">donnied</div>
          <div className="hidden text-xs text-zinc-500 sm:block">dashboard</div>
        </div>
        <div className="flex min-w-0 items-center gap-2 text-xs text-zinc-500">
          {state && <StatusBadge connected={state.device.connected} state={state.device.state} />}
          <span className="hidden md:inline">{lastUpdated ? `Updated ${formatClock(lastUpdated)}` : 'Not updated'}</span>
          <IconButton onClick={onRefresh} disabled={loading} label={loading ? 'Loading' : 'Refresh'}><RefreshCw size={14} className={loading ? 'animate-spin' : ''} /></IconButton>
        </div>
      </div>
    </header>
  );
}

function DeviceHero({ state, busy, onScan, onAction }: { state: AppState; busy: BusyAction; onScan: () => Promise<void>; onAction: (label: string, action: () => Promise<ActionResult>) => Promise<ActionResult> }) {
  return (
    <Panel>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="mb-2 flex flex-wrap items-center gap-2"><StatusBadge connected={state.device.connected} state={state.device.state} /></div>
          <h1 className="truncate text-xl font-semibold tracking-tight sm:text-2xl">{deviceTitle(state)}</h1>
          <div className="mt-1 flex min-w-0 items-center gap-1.5 text-xs text-zinc-500 sm:text-sm">
            <Bluetooth size={14} className="shrink-0" />
            <span className="truncate">{state.device.paired_addr || 'No paired address'}</span>
          </div>
        </div>
        <div className="grid grid-cols-3 gap-2 sm:flex sm:shrink-0">
          <Button tone="primary" onClick={onScan} disabled={Boolean(busy)} icon={<Search size={14} />}>Scan</Button>
          <Button onClick={() => onAction('Syncing', () => postAction('sync', { kinds: ['all'] }))} disabled={Boolean(busy)}>Sync</Button>
          <Button onClick={() => onAction('Finding', () => postAction('find-device'))} disabled={Boolean(busy) || !state.device.connected} icon={<Radar size={14} />}>Find</Button>
        </div>
      </div>
    </Panel>
  );
}

function DeviceActions({ state, busy, onAction }: { state: AppState; busy: BusyAction; onAction: (label: string, action: () => Promise<ActionResult>) => Promise<ActionResult> }) {
  const disabled = Boolean(busy);
  return (
    <Panel title="Device controls" subtitle={busy ? `${busy}…` : 'Ready'}>
      <div className="grid gap-3">
        <dl className="grid gap-2 text-sm">
          <Fact label="Device" value={deviceTitle(state)} />
          <Fact label="Last seen" value={formatDateTime(state.device.last_seen)} />
          <Fact label="Battery" value={state.device.battery ? `${state.device.battery.level}%${state.device.battery.charging ? ' charging' : ''}` : '—'} />
          <Fact label="Daemon" value={`${state.process.pid} · ${state.process.uptime}`} />
        </dl>
        <div className="grid grid-cols-2 gap-2">
          <Button onClick={() => onAction('Reading HR', () => postAction('realtime-read', { type: REALTIME.heartRate }))} disabled={disabled || !state.device.connected}>Read HR</Button>
          <Button onClick={() => onAction('Reading SpO₂', () => postAction('realtime-read', { type: REALTIME.spo2 }))} disabled={disabled || !state.device.connected}>Read SpO₂</Button>
          <Button onClick={() => onAction('Syncing time', () => postAction('sync-time'))} disabled={disabled || !state.device.connected}>Sync time</Button>
          <Button onClick={() => onAction('Syncing', () => postAction('sync', { kinds: ['all'] }))} disabled={disabled}>Sync all</Button>
          <Button tone="danger" className="col-span-2" onClick={() => onAction('Unpairing', () => postAction('unpair'))} disabled={disabled || !state.device.paired_addr}>Unpair</Button>
        </div>
      </div>
    </Panel>
  );
}

function ScanPanel({ results, busy, pairedAddr, onScan, onPair }: { results: ScanResult[]; busy: BusyAction; pairedAddr: string; onScan: () => Promise<void>; onPair: (device: ScanResult) => Promise<void> }) {
  return (
    <Panel title="Bluetooth" subtitle={results.length ? `${results.length} nearby` : 'scan results'}>
      <div className="mb-3 flex items-center justify-between gap-2">
        <span className="text-sm text-zinc-500">Pair or switch device</span>
        <Button tone="primary" onClick={onScan} disabled={Boolean(busy)} icon={<Search size={14} />}>Scan</Button>
      </div>
      {results.length === 0 ? (
        <div className="border border-dashed border-zinc-300 bg-zinc-50 p-4 text-center text-sm text-zinc-500">No scan results yet.</div>
      ) : (
        <div className="divide-y divide-zinc-200 border border-zinc-200">
          {results.map((result) => (
            <div key={result.address} className="grid grid-cols-[1fr_auto] gap-3 p-3">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{result.name || 'Unnamed device'}</div>
                <div className="truncate text-xs text-zinc-500">{result.address}</div>
                <div className="mt-1 text-xs text-zinc-500">RSSI {result.rssi} dBm</div>
              </div>
              <Button onClick={() => onPair(result)} disabled={Boolean(busy) || pairedAddr === result.address}>{pairedAddr === result.address ? 'Paired' : 'Pair'}</Button>
            </div>
          ))}
        </div>
      )}
    </Panel>
  );
}

function SleepSummary({ state }: { state: AppState }) {
  const latest = latestSleepSession(state.history.sleep_sessions ?? []);
  const stageMinutes = summarizeSleepStages(latest?.segments ?? []);
  return (
    <div className="mb-3 grid gap-3 border-b border-zinc-200 pb-3">
      <div className="grid grid-cols-3 gap-2">
        <TinyStat label="Duration" value={latest ? formatDuration(latest.total_minutes) : '—'} />
        <TinyStat label="Start" value={latest ? formatShortTime(latest.start) : '—'} />
        <TinyStat label="End" value={latest ? formatShortTime(latest.end) : '—'} />
      </div>
      <div className="grid grid-cols-2 gap-2 text-xs text-zinc-600 sm:grid-cols-4">
        {['awake', 'rem', 'core', 'deep'].map((stage) => (
          <span key={stage} className="inline-flex min-w-0 items-center gap-1.5"><i className="size-2.5 shrink-0" style={{ background: sleepPalette[stage] }} /><span className="truncate">{sleepStageLabels[stage]} {stageMinutes[stage] ? formatDuration(stageMinutes[stage]) : ''}</span></span>
        ))}
      </div>
    </div>
  );
}

function DataPanel({ state }: { state: AppState }) {
  const errors = Object.entries(state.history.errors ?? {});
  const hr = state.history.hr_samples ?? [];
  const steps = state.history.step_details ?? [];
  const sleep = state.history.sleep_sessions ?? [];
  return (
    <Panel title="Data" subtitle={historyRangeLabel(state)}>
      <div className="grid gap-3">
        <div className="grid grid-cols-3 divide-x divide-zinc-200 border border-zinc-200 text-center text-xs">
          <MiniCount label="HR" value={hr.length} />
          <MiniCount label="Steps" value={steps.length} />
          <MiniCount label="Sleep" value={sleep.length} />
        </div>
        <dl className="grid gap-2 text-sm">
          <Fact label="Latest HR" value={hr.length ? formatDateTime(hr[hr.length - 1].time) : '—'} />
          <Fact label="Latest sleep" value={sleep.length ? formatDateTime(latestSleepSession(sleep)?.end) : '—'} />
          <Fact label="State" value={`${titleCase(state.sleep.state)}${state.sleep.stale ? ' · stale' : ''}`} />
        </dl>
        {errors.length > 0 ? (
          <ul className="space-y-2 text-sm">
            {errors.map(([key, value]) => <li className="border border-rose-200 bg-rose-50 p-2 text-rose-800" key={key}><strong>{key}</strong>: {value}</li>)}
          </ul>
        ) : <p className="border border-emerald-200 bg-emerald-50 p-2 text-sm text-emerald-800">No sync errors.</p>}
      </div>
    </Panel>
  );
}

function Panel({ title, subtitle, className = '', children }: { title?: string; subtitle?: string; className?: string; children: ComponentChildren }) {
  return (
    <section className={`min-w-0 overflow-hidden border border-zinc-200 bg-white shadow-sm ${className}`}>
      {(title || subtitle) && (
        <div className="border-b border-zinc-200 px-3 py-2.5">
          <div className="flex min-w-0 items-baseline justify-between gap-3">
            {title && <h2 className="truncate text-sm font-semibold tracking-tight">{title}</h2>}
            {subtitle && <p className="truncate text-xs text-zinc-500">{subtitle}</p>}
          </div>
        </div>
      )}
      <div className="p-3">{children}</div>
    </section>
  );
}

function ChartPanel({ title, subtitle, children }: { title: string; subtitle: string; children: ComponentChildren }) {
  return <Panel title={title} subtitle={subtitle} className="min-w-0">{children}</Panel>;
}

function Metric({ icon, label, value, unit, detail, className = '' }: { icon: ComponentChildren; label: string; value: string; unit?: string; detail: string; className?: string }) {
  return (
    <section className={`min-w-0 border border-zinc-200 bg-white p-3 shadow-sm ${className}`}>
      <div className="flex items-center justify-between gap-2 text-xs text-zinc-500"><span className="truncate">{label}</span><span className="shrink-0 text-zinc-400">{icon}</span></div>
      <div className="mt-2 flex min-w-0 items-baseline gap-1"><span className="truncate text-xl font-semibold tracking-tight sm:text-2xl">{value}</span>{unit && <span className="shrink-0 text-xs text-zinc-500">{unit}</span>}</div>
      <div className="mt-1 truncate text-xs text-zinc-500">{detail}</div>
    </section>
  );
}

function Button({ children, onClick, disabled, tone = 'default', icon, className = '' }: { children: ComponentChildren; onClick?: () => void | Promise<unknown>; disabled?: boolean; tone?: Tone; icon?: ComponentChildren; className?: string }) {
  const toneClass = tone === 'primary'
    ? 'border-zinc-900 bg-zinc-900 text-white hover:bg-zinc-800'
    : tone === 'danger'
      ? 'border-rose-300 bg-white text-rose-700 hover:bg-rose-50'
      : 'border-zinc-300 bg-white text-zinc-700 hover:bg-zinc-50';
  return <button className={`inline-flex min-h-9 items-center justify-center gap-1.5 border px-2.5 py-1.5 text-xs font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${toneClass} ${className}`} disabled={disabled} onClick={() => void onClick?.()}>{icon}{children}</button>;
}

function IconButton({ children, onClick, disabled, label }: { children: ComponentChildren; onClick: () => void; disabled?: boolean; label: string }) {
  return <button className="grid size-8 place-items-center border border-zinc-300 bg-white text-zinc-700 disabled:opacity-50 md:w-auto md:px-2 md:text-xs" aria-label={label} title={label} disabled={disabled} onClick={onClick}>{children}<span className="hidden md:ml-1.5 md:inline">{label}</span></button>;
}

function StatusBadge({ connected, state }: { connected: boolean; state: string }) {
  return <span className={`inline-flex max-w-[140px] items-center gap-1.5 border px-2 py-0.5 text-xs font-medium sm:max-w-none ${connected ? 'border-emerald-300 bg-emerald-50 text-emerald-700' : 'border-zinc-300 bg-zinc-50 text-zinc-600'}`}><span className={`size-1.5 shrink-0 ${connected ? 'bg-emerald-500' : 'bg-zinc-400'}`} /><span className="truncate">{connected ? 'Connected' : state || 'Disconnected'}</span></span>;
}

function Alert({ tone, title, body }: { tone: 'error' | 'success'; title: string; body: string }) {
  return <div className={`border px-3 py-2 text-sm ${tone === 'error' ? 'border-rose-200 bg-rose-50 text-rose-800' : 'border-emerald-200 bg-emerald-50 text-emerald-800'}`}><strong>{title}</strong><span className="ml-2">{body}</span></div>;
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div className="grid grid-cols-[78px_1fr] gap-2"><dt className="text-zinc-500">{label}</dt><dd className="truncate font-medium">{value}</dd></div>;
}

function TinyStat({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0 border border-zinc-200 bg-zinc-50 p-2"><div className="truncate text-[11px] uppercase tracking-wide text-zinc-500">{label}</div><div className="mt-1 truncate text-sm font-semibold">{value}</div></div>;
}

function MiniCount({ label, value }: { label: string; value: number }) {
  return <div className="p-2.5"><div className="font-semibold">{formatNumber(value)}</div><div className="text-[11px] text-zinc-500">{label}</div></div>;
}

function ChartEmpty({ hasData, label, children }: { hasData: boolean; label: string; children: ComponentChildren }) {
  if (!hasData) return <div className="grid min-h-[220px] place-items-center border border-dashed border-zinc-300 bg-zinc-50 px-4 text-center text-sm text-zinc-500">{label}</div>;
  return <>{children}</>;
}

function TimeSeriesChart({ option, className = '' }: { option: EChartsOption; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const chartRef = useRef<ReturnType<typeof echarts.init> | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    chartRef.current = echarts.init(ref.current, undefined, { renderer: 'canvas' });
    const observer = new ResizeObserver(() => chartRef.current?.resize());
    observer.observe(ref.current);
    return () => {
      observer.disconnect();
      chartRef.current?.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    chartRef.current?.setOption(option, true);
  }, [option]);

  return <div ref={ref} className={`echart ${className}`} />;
}

function heartRateOption(samples: HRSample[]): EChartsOption {
  const data = samples.filter((sample) => sample.bpm > 0).map((sample) => [sample.time, sample.bpm]);
  return baseOption({
    tooltip: { trigger: 'axis', formatter: (params: any) => chartTooltip('Heart rate', params, 'bpm') },
    xAxis: { type: 'time', axisLabel: { formatter: (value: number) => formatAxisDate(value), color: '#71717a', hideOverlap: true, showMinLabel: false, showMaxLabel: false } },
    yAxis: { type: 'value', name: 'bpm', min: 'dataMin', max: 'dataMax' },
    series: [{ type: 'line', data, smooth: true, showSymbol: false, lineStyle: { width: 2, color: '#be123c' }, areaStyle: { color: 'rgba(190,18,60,0.06)' } }],
  });
}

function stepsOption(details: StepDetail[]): EChartsOption {
  const byHour = new Map<number, number>();
  for (const detail of details) {
    const ts = new Date(detail.year, detail.month - 1, detail.day, detail.hour, 0, 0, 0).getTime();
    byHour.set(ts, (byHour.get(ts) ?? 0) + detail.steps);
  }
  const data = Array.from(byHour.entries()).sort(([a], [b]) => a - b);
  return baseOption({
    tooltip: { trigger: 'axis', formatter: (params: any) => chartTooltip('Steps', params, 'steps') },
    xAxis: { type: 'time', axisLabel: { formatter: (value: number) => formatAxisDate(value), color: '#71717a', hideOverlap: true, showMinLabel: false, showMaxLabel: false } },
    yAxis: { type: 'value' },
    series: [{ type: 'bar', data, barMaxWidth: 14, itemStyle: { color: '#3f3f46' } }],
  });
}

function sleepStageOption(sessions: SleepSession[]): EChartsOption {
  const rows = sleepRows(sessions);
  const data = rows.flatMap((row, rowIndex) => row.segments.map((segment) => [segment.startMinute, segment.endMinute, rowIndex, segment.stage, segment.minutes, segment.startMs, segment.endMs]));
  return baseOption({
    grid: { top: 12, right: 4, bottom: 30, left: 42 },
    tooltip: { formatter: (params: any) => `<strong>${sleepStageLabels[params.value[3]]}</strong><br/>${formatDateTimeFromMs(params.value[5])} → ${formatDateTimeFromMs(params.value[6])}<br/>${formatDuration(params.value[4])}` },
    xAxis: { type: 'value', min: 0, max: SLEEP_WINDOW_HOURS * 60, interval: 360, axisLabel: { formatter: (value: number) => sleepMinuteLabel(value), color: '#71717a', hideOverlap: true } },
    yAxis: { type: 'category', data: rows.map((row) => row.label), inverse: true, axisTick: { show: false } },
    series: [{
      type: 'custom', data, encode: { x: [0, 1], y: 2 },
      renderItem: (params: any, api: any) => {
        const stage = api.value(3) as string;
        const start = api.coord([api.value(0), api.value(2)]);
        const end = api.coord([api.value(1), api.value(2)]);
        const bandHeight = api.size([0, 1])[1] * 0.58;
        const rect = echarts.graphic.clipRectByRect(
          { x: start[0], y: start[1] - bandHeight / 2, width: Math.max(1, end[0] - start[0]), height: bandHeight },
          { x: params.coordSys.x, y: params.coordSys.y, width: params.coordSys.width, height: params.coordSys.height },
        );
        return rect && { type: 'rect', shape: rect, style: { fill: sleepPalette[stage] ?? sleepPalette.unknown } };
      },
    }],
  });
}

function baseOption(option: EChartsOption): EChartsOption {
  return {
    animation: false,
    color: ['#3f3f46'],
    textStyle: { fontFamily: 'Inter, ui-sans-serif, system-ui, sans-serif', color: '#3f3f46', fontSize: 12 },
    grid: { top: 10, right: 4, bottom: 32, left: 34, containLabel: true },
    tooltip: { borderWidth: 1, borderColor: '#d4d4d8', backgroundColor: 'rgba(255,255,255,0.98)', textStyle: { color: '#18181b', fontSize: 12 } },
    xAxis: { axisLine: { lineStyle: { color: '#d4d4d8' } }, axisTick: { show: false }, axisLabel: { color: '#71717a' }, splitLine: { lineStyle: { color: '#f4f4f5' } } },
    yAxis: { axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: '#71717a' }, splitLine: { lineStyle: { color: '#f4f4f5' } } },
    ...option,
  };
}

function displayMetrics(state: AppState, realtime: RealtimeValues) {
  const latestHR = state.device.heart_rate || realtime[REALTIME.heartRate] || latestHRSample(state.history.hr_samples ?? [])?.bpm || 0;
  const currentSpO2 = state.device.spo2 || realtime[REALTIME.spo2] || 0;
  const spo2Range = latestSpO2Range(state);
  const stepSummary = stepsSummary(state);
  const latestSleep = latestSleepSession(state.history.sleep_sessions ?? []);
  return {
    battery: { value: state.device.battery ? `${state.device.battery.level}%` : '—', detail: state.device.battery?.charging ? 'charging' : 'not charging' },
    hr: { value: latestHR ? String(latestHR) : '—', unit: latestHR ? 'bpm' : undefined, detail: state.device.heart_rate ? 'snapshot' : realtime[REALTIME.heartRate] ? 'manual reading' : latestHR ? 'history' : 'no reading' },
    steps: { value: formatNumber(stepSummary.steps), detail: `${stepSummary.source} · ${formatDistance(stepSummary.distance)}` },
    sleep: { value: latestSleep ? formatDuration(latestSleep.total_minutes) : '—', detail: latestSleep ? `${formatShortDate(latestSleep.start)} night` : titleCase(state.sleep.state) },
    spo2: { value: currentSpO2 ? `${currentSpO2}%` : spo2Range.value || '—', detail: currentSpO2 ? 'manual/watch reading' : spo2Range.detail },
  };
}

function stepsSummary(state: AppState) {
  if ((state.device.steps ?? 0) > 0) return { steps: state.device.steps ?? 0, calories: state.device.calories ?? 0, distance: state.device.distance ?? 0, source: 'snapshot' };
  const details = state.history.step_details ?? [];
  return {
    steps: details.reduce((sum, d) => sum + d.steps, 0),
    calories: details.reduce((sum, d) => sum + d.calories, 0),
    distance: details.reduce((sum, d) => sum + d.distance, 0),
    source: details.length ? 'history' : 'no data',
  };
}

function latestHRSample(samples: HRSample[]) {
  return samples.filter((sample) => sample.bpm > 0).sort((a, b) => new Date(a.time).getTime() - new Date(b.time).getTime()).at(-1);
}

function latestSpO2Range(state: AppState) {
  const day = [...(state.history.spo2_days ?? [])].sort((a, b) => a.days_ago - b.days_ago)[0];
  const samples = day?.samples?.filter((sample) => sample.min > 0 && sample.max > 0) ?? [];
  if (samples.length === 0) return { value: '', detail: 'no SpO₂ history' };
  const min = Math.min(...samples.map((sample) => sample.min));
  const max = Math.max(...samples.map((sample) => sample.max));
  return { value: min === max ? `${max}%` : `${min}-${max}%`, detail: `history (${samples.length})` };
}

function sleepRows(sessions: SleepSession[]) {
  return [...sessions].sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime()).map((session) => {
    const anchor = sleepAnchor(session.end);
    const segments = session.segments.map((segment) => {
      const startMs = new Date(segment.start).getTime();
      const endMs = new Date(segment.end).getTime();
      return {
        startMs,
        endMs,
        startMinute: clamp((startMs - anchor.getTime()) / 60000, 0, SLEEP_WINDOW_HOURS * 60),
        endMinute: clamp((endMs - anchor.getTime()) / 60000, 0, SLEEP_WINDOW_HOURS * 60),
        stage: normalizeStage(segment.stage),
        minutes: segment.minutes,
      };
    }).filter((segment) => segment.endMinute > segment.startMinute);
    return { label: formatShortDate(session.end), segments };
  }).filter((row) => row.segments.length > 0);
}

function sleepSegments(sessions: SleepSession[]) { return sleepRows(sessions).flatMap((row) => row.segments); }

function sleepAnchor(sessionEnd: string) {
  const end = new Date(sessionEnd);
  const anchor = new Date(end);
  anchor.setHours(SLEEP_WINDOW_START_HOUR, 0, 0, 0);
  anchor.setDate(anchor.getDate() - 1);
  return anchor;
}

function normalizeStage(stage: string) {
  const s = stage.toLowerCase();
  if (s.includes('deep')) return 'deep';
  if (s.includes('light') || s.includes('core')) return 'core';
  if (s.includes('rem')) return 'rem';
  if (s.includes('awake') || s.includes('wake')) return 'awake';
  return 'unknown';
}

function latestSleepSession(sessions: SleepSession[]) {
  return [...sessions].sort((a, b) => new Date(a.end).getTime() - new Date(b.end).getTime()).at(-1);
}

function summarizeSleepStages(segments: SleepSession['segments']) {
  return segments.reduce<Record<string, number>>((out, segment) => {
    const stage = normalizeStage(segment.stage);
    out[stage] = (out[stage] ?? 0) + segment.minutes;
    return out;
  }, {});
}

function deviceTitle(state: AppState) {
  if (state.device.paired_name) return state.device.paired_name;
  if (state.device.paired_addr) return state.device.connected ? 'Connected device' : 'Paired device';
  return 'No device paired';
}

function historyRangeLabel(state: AppState) {
  if (state.history.from || state.history.to) return `${formatShortDate(state.history.from)} → ${formatShortDate(state.history.to)}`;
  return state.history.selected_day;
}

function chartTooltip(label: string, params: any, unit: string) {
  const point = Array.isArray(params) ? params[0] : params;
  const value = point?.value;
  const x = Array.isArray(value) ? value[0] : point?.axisValue;
  const y = Array.isArray(value) ? value[1] : value;
  return `<strong>${label}</strong><br/>${formatDateTimeFromMs(new Date(x).getTime())}<br/>${formatNumber(Number(y))} ${unit}`;
}

function sleepMinuteLabel(value: number) {
  const total = SLEEP_WINDOW_START_HOUR * 60 + value;
  const hour = Math.floor(total / 60) % 24;
  return `${hour}:00`;
}

function clamp(value: number, min: number, max: number) { return Math.max(min, Math.min(max, value)); }
function realtimeName(kind: number) { return kind === REALTIME.heartRate ? 'HR' : kind === REALTIME.spo2 ? 'SpO₂' : `Realtime ${kind}`; }
function formatNumber(value: number) { return new Intl.NumberFormat().format(value); }
function formatDistance(meters: number) { return meters >= 1000 ? `${(meters / 1000).toFixed(2)} km` : `${meters} m`; }
function formatDuration(minutes: number) { if (!minutes) return '0m'; const h = Math.floor(minutes / 60); const m = minutes % 60; return h ? `${h}h ${m}m` : `${m}m`; }
function formatDateTime(value?: string) { return value ? new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : '—'; }
function formatDateTimeFromMs(value: number) { return new Date(value).toLocaleString([], { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }); }
function formatShortDate(value?: string) { return value ? new Date(value).toLocaleDateString([], { month: 'short', day: 'numeric' }) : '—'; }
function formatShortTime(value?: string) { return value ? new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) : '—'; }
function formatAxisDate(value: number) { return new Date(value).toLocaleString([], { month: 'numeric', day: 'numeric', hour: 'numeric' }); }
function formatClock(value: Date) { return value.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' }); }
function titleCase(value: string) { return value.replace(/_/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase()); }

render(<App />, document.getElementById('app')!);
