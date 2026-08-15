// Mirrors of the backend API types (backend/internal/{store,driver,profile}).

export type Engine = 'postgresql' | 'mysql' | 'mongodb';

export type RunStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled';

export interface RunSpec {
  profile: string;
  threads: number;
  durationSeconds: number;
  readPercent: number;
  writePercent: number;
  tables: number;
  tableSize: number;
  records: number;
  skipPrepare?: boolean;
  skipCleanup?: boolean;
  extra?: Record<string, string>;
}

export interface OpStats {
  ops: number;
  count: number;
  avgMs: number;
  p99Ms: number;
}

export interface BenchResult {
  throughputOps: number;
  qps?: number;
  latencyAvgMs: number;
  latencyP95Ms: number;
  latencyP99Ms?: number;
  latencyMaxMs?: number;
  totalOps: number;
  errors: number;
  perOperation?: Record<string, OpStats>;
}

export interface Fingerprint {
  engine: string;
  engineVersion?: string;
  replicas?: number;
  cpuLimit?: string;
  memoryLimit?: string;
  storageClass?: string;
  storageSize?: string;
  driver: string;
  driverImage: string;
  generatorNode?: string;
  databaseNodes?: string[];
  isolated?: boolean;
  spec: RunSpec;
}

export interface Run {
  id: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  namespace: string;
  instanceName: string;
  engine: Engine;
  driver: string;
  profile: string;
  status: RunStatus;
  message?: string;
  jobName?: string;
  spec: RunSpec;
  result?: BenchResult;
  fingerprint?: Fingerprint;
}

export interface Profile {
  name: string;
  displayName: string;
  description: string;
  spec: RunSpec;
}

export interface DriverInfo {
  name: string;
  image: string;
  engines: Engine[];
}

export interface Instance {
  namespace: string;
  name: string;
  engine: Engine;
  version?: string;
  status?: string;
  hostname?: string;
  port?: number;
}

export interface Comparison {
  a: Run;
  b: Run;
  deltas: Record<string, number>;
  comparable: boolean;
  differences: string[];
}

// The subset of the host-provided plugin API the components need. In
// production this is api.fetch (proxied + authenticated by the host); in the
// dev sandbox it is a plain fetch against the local backend.
export type PluginFetch = (path: string, init?: RequestInit) => Promise<Response>;
