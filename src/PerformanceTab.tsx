import { useCallback, useEffect, useMemo, useState } from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Checkbox from '@mui/material/Checkbox';
import CssBaseline from '@mui/material/CssBaseline';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Typography from '@mui/material/Typography';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import { PluginFetchContext, api, usePluginFetch } from './api';
import { CompareDialog } from './CompareDialog';
import { NewRunDialog } from './NewRunDialog';
import { RunDetail } from './RunDetail';
import { TrendCharts } from './TrendCharts';
import { StatusChip, fmtDate, fmtNum } from './components';
import type { Engine, PluginFetch, Run } from './types';

const theme = createTheme({
  palette: { primary: { main: '#0e5fb5' } },
  typography: { fontFamily: 'Poppins, Roboto, Helvetica, Arial, sans-serif' },
});

export interface PerformanceTabProps {
  namespace: string;
  instanceName: string;
  // Engine may be unknown to the host; the backend resolves it from the CR.
  engine?: Engine;
  pluginFetch: PluginFetch;
}

function RunsView({ namespace, instanceName, engine }: { namespace: string; instanceName: string; engine?: Engine }) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState('');
  const [resolvedEngine, setResolvedEngine] = useState<Engine | undefined>(engine);
  const [selectedRun, setSelectedRun] = useState<Run | null>(null);
  const [newRunOpen, setNewRunOpen] = useState(false);
  const [compareSel, setCompareSel] = useState<string[]>([]);
  const [compareOpen, setCompareOpen] = useState(false);
  const fetch_ = usePluginFetch();

  const refresh = useCallback(() => {
    api.listRuns(fetch_, namespace, instanceName)
      .then((rs) => { setRuns(rs); setError(''); })
      .catch((e) => setError(String(e.message ?? e)));
  }, [fetch_, namespace, instanceName]);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 4000);
    return () => clearInterval(t);
  }, [refresh]);

  // Resolve engine from the backend when the host did not provide it.
  useEffect(() => {
    if (resolvedEngine) return;
    api.instances(fetch_, namespace)
      .then((list) => {
        const inst = list.find((i) => i.name === instanceName);
        if (inst?.engine) setResolvedEngine(inst.engine);
      })
      .catch(() => { /* standalone mode: engine picked at run creation */ });
  }, [resolvedEngine, fetch_, namespace, instanceName]);

  // Keep the detail view in sync with polling.
  useEffect(() => {
    if (!selectedRun) return;
    const fresh = runs.find((r) => r.id === selectedRun.id);
    if (fresh && fresh !== selectedRun) setSelectedRun(fresh);
  }, [runs, selectedRun]);

  const toggleCompare = (id: string) => {
    setCompareSel((sel) => sel.includes(id)
      ? sel.filter((s) => s !== id)
      : [...sel.slice(-1), id]); // keep at most 2
  };

  const cancelRun = async (run: Run) => {
    try {
      await api.cancelRun(fetch_, run.id);
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const succeeded = useMemo(() => runs.filter((r) => r.status === 'succeeded'), [runs]);

  if (selectedRun) {
    return <RunDetail run={selectedRun} onBack={() => setSelectedRun(null)} onCancel={cancelRun} />;
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', mb: 2, gap: 2 }}>
        <Typography variant="h6" sx={{ flex: 1 }}>Performance runs</Typography>
        <Button
          variant="outlined"
          disabled={compareSel.length !== 2}
          onClick={() => setCompareOpen(true)}
        >
          Compare {compareSel.length ? `(${compareSel.length}/2)` : ''}
        </Button>
        <Button variant="contained" onClick={() => setNewRunOpen(true)}>New run</Button>
      </Box>

      {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}

      <TrendCharts runs={runs} />

      {runs.length === 0 && !error ? (
        <Alert severity="info">
          No benchmark runs yet for {instanceName}. Start one with “New run” — pick a workload
          profile and the plugin does the rest: credentials, an isolated benchmark job, and
          normalized results you can compare over time.
        </Alert>
      ) : (
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell padding="checkbox" />
              <TableCell>Started</TableCell>
              <TableCell>Profile</TableCell>
              <TableCell>Driver</TableCell>
              <TableCell>Status</TableCell>
              <TableCell align="right">Throughput (ops/s)</TableCell>
              <TableCell align="right">p95 (ms)</TableCell>
              <TableCell align="right">Errors</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {runs.map((r) => (
              <TableRow
                key={r.id}
                hover
                onClick={() => setSelectedRun(r)}
                sx={{ cursor: 'pointer' }}
              >
                <TableCell padding="checkbox" onClick={(e) => e.stopPropagation()}>
                  <Checkbox
                    size="small"
                    disabled={r.status !== 'succeeded' && !compareSel.includes(r.id)}
                    checked={compareSel.includes(r.id)}
                    onChange={() => toggleCompare(r.id)}
                  />
                </TableCell>
                <TableCell>{fmtDate(r.createdAt)}</TableCell>
                <TableCell>{r.profile}</TableCell>
                <TableCell>{r.driver}</TableCell>
                <TableCell><StatusChip status={r.status} /></TableCell>
                <TableCell align="right">{fmtNum(r.result?.throughputOps)}</TableCell>
                <TableCell align="right">{fmtNum(r.result?.latencyP95Ms, 2)}</TableCell>
                <TableCell align="right">{fmtNum(r.result?.errors, 0)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {succeeded.length >= 1 && compareSel.length < 2 && (
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>
          Select two completed runs to compare them. Comparisons warn when the runs were
          measured under different conditions.
        </Typography>
      )}

      <NewRunDialog
        open={newRunOpen}
        onClose={() => setNewRunOpen(false)}
        onCreated={() => refresh()}
        namespace={namespace}
        instanceName={instanceName}
        engine={resolvedEngine ?? 'postgresql'}
      />
      <CompareDialog
        open={compareOpen}
        onClose={() => setCompareOpen(false)}
        aID={compareSel[0] ?? ''}
        bID={compareSel[1] ?? ''}
      />
    </Box>
  );
}

export default function PerformanceTab(props: PerformanceTabProps) {
  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <PluginFetchContext.Provider value={props.pluginFetch}>
        <Box sx={{ p: 2 }}>
          <RunsView
            namespace={props.namespace}
            instanceName={props.instanceName}
            engine={props.engine}
          />
        </Box>
      </PluginFetchContext.Provider>
    </ThemeProvider>
  );
}
