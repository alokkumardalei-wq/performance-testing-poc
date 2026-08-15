import { createContext, useContext } from 'react';
import type {
  Comparison, DriverInfo, Instance, PluginFetch, Profile, Run, RunSpec,
} from './types';

// api.fetch is handed to the plugin's isolated React tree via props → context
// (not a window global): the wrapper component receives it from register()
// and provides it here.
export const PluginFetchContext = createContext<PluginFetch | null>(null);

export function usePluginFetch(): PluginFetch {
  const f = useContext(PluginFetchContext);
  if (!f) throw new Error('PluginFetchContext not provided');
  return f;
}

async function asJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch { /* keep status text */ }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export interface CreateRunPayload {
  namespace: string;
  instanceName: string;
  profile: string;
  driver?: string;
  overrides?: Partial<RunSpec>;
}

export const api = {
  profiles: (f: PluginFetch) =>
    f('/api/profiles').then(asJSON<{ profiles: Profile[] }>).then((r) => r.profiles),
  drivers: (f: PluginFetch) =>
    f('/api/drivers').then(asJSON<{ drivers: DriverInfo[] }>).then((r) => r.drivers),
  instances: (f: PluginFetch, namespace?: string) =>
    f(`/api/instances${namespace ? `?namespace=${encodeURIComponent(namespace)}` : ''}`)
      .then(asJSON<{ instances: Instance[] }>).then((r) => r.instances),
  listRuns: (f: PluginFetch, namespace?: string, instance?: string) => {
    const q = new URLSearchParams();
    if (namespace) q.set('namespace', namespace);
    if (instance) q.set('instance', instance);
    const qs = q.toString();
    return f(`/api/runs${qs ? `?${qs}` : ''}`)
      .then(asJSON<{ runs: Run[] }>).then((r) => r.runs);
  },
  getRun: (f: PluginFetch, id: string) => f(`/api/runs/${id}`).then(asJSON<Run>),
  getOutput: (f: PluginFetch, id: string) =>
    f(`/api/runs/${id}/output`).then((r) => (r.ok ? r.text() : Promise.resolve(''))),
  createRun: (f: PluginFetch, payload: CreateRunPayload) =>
    f('/api/runs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }).then(asJSON<Run>),
  cancelRun: (f: PluginFetch, id: string) =>
    f(`/api/runs/${id}/cancel`, { method: 'POST' }).then(asJSON<Run>),
  deleteRun: (f: PluginFetch, id: string) => f(`/api/runs/${id}`, { method: 'DELETE' }),
  compare: (f: PluginFetch, a: string, b: string) =>
    f(`/api/compare?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}`).then(asJSON<Comparison>),
};
