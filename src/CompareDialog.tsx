import { useEffect, useState } from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Typography from '@mui/material/Typography';
import { api, usePluginFetch } from './api';
import { fmtDate, fmtNum } from './components';
import type { Comparison } from './types';

// For latency lower is better; for throughput higher is better. deltaGood
// maps the sign onto "improved"/"regressed" per metric.
const metricRows: { key: string; label: string; unit: string; higherIsBetter: boolean; get: (c: Comparison, side: 'a' | 'b') => number | undefined }[] = [
  { key: 'throughputOps', label: 'Throughput', unit: 'ops/s', higherIsBetter: true, get: (c, s) => c[s].result?.throughputOps },
  { key: 'qps', label: 'Queries', unit: 'q/s', higherIsBetter: true, get: (c, s) => c[s].result?.qps },
  { key: 'latencyAvgMs', label: 'Latency avg', unit: 'ms', higherIsBetter: false, get: (c, s) => c[s].result?.latencyAvgMs },
  { key: 'latencyP95Ms', label: 'Latency p95', unit: 'ms', higherIsBetter: false, get: (c, s) => c[s].result?.latencyP95Ms },
];

export function CompareDialog({ open, onClose, aID, bID }: {
  open: boolean; onClose: () => void; aID: string; bID: string;
}) {
  const fetch_ = usePluginFetch();
  const [cmp, setCmp] = useState<Comparison | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open || !aID || !bID) return;
    setCmp(null);
    setError('');
    api.compare(fetch_, aID, bID).then(setCmp).catch((e) => setError(String(e.message ?? e)));
  }, [open, aID, bID, fetch_]);

  return (
    <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth>
      <DialogTitle>Compare runs</DialogTitle>
      <DialogContent>
        {error && <Alert severity="error">{error}</Alert>}
        {cmp && (
          <>
            {!cmp.comparable && (
              <Alert severity="warning" sx={{ mb: 2 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  These runs were measured under different conditions — deltas below are not a
                  like-for-like comparison.
                </Typography>
                <ul style={{ margin: '4px 0 0', paddingLeft: 18 }}>
                  {cmp.differences.map((d) => <li key={d}><Typography variant="caption">{d}</Typography></li>)}
                </ul>
              </Alert>
            )}
            <Table size="small">
              <TableHead>
                <TableRow>
                  <TableCell>Metric</TableCell>
                  <TableCell align="right">A · {fmtDate(cmp.a.createdAt)}</TableCell>
                  <TableCell align="right">B · {fmtDate(cmp.b.createdAt)}</TableCell>
                  <TableCell align="right">Δ (A→B)</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {metricRows.map((m) => {
                  const av = m.get(cmp, 'a');
                  const bv = m.get(cmp, 'b');
                  if (!av && !bv) return null;
                  const delta = cmp.deltas[m.key];
                  const improved = delta !== undefined && (m.higherIsBetter ? delta > 0 : delta < 0);
                  return (
                    <TableRow key={m.key}>
                      <TableCell>{m.label} <Typography component="span" variant="caption" color="text.secondary">({m.unit})</Typography></TableCell>
                      <TableCell align="right">{fmtNum(av, 2)}</TableCell>
                      <TableCell align="right">{fmtNum(bv, 2)}</TableCell>
                      <TableCell align="right" sx={{
                        color: delta === undefined || Math.abs(delta) < 1 ? 'text.secondary'
                          : improved ? 'success.main' : 'error.main',
                        fontWeight: 600,
                      }}>
                        {delta === undefined ? '—' : `${delta > 0 ? '+' : ''}${delta.toFixed(1)}%`}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
  );
}
