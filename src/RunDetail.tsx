import { useEffect, useState } from 'react';
import Accordion from '@mui/material/Accordion';
import AccordionDetails from '@mui/material/AccordionDetails';
import AccordionSummary from '@mui/material/AccordionSummary';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Divider from '@mui/material/Divider';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Typography from '@mui/material/Typography';
import { api, usePluginFetch } from './api';
import { StatTile, StatusChip, fmtDate, fmtNum } from './components';
import type { Run } from './types';

export function RunDetail({ run, onBack, onCancel }: {
  run: Run;
  onBack: () => void;
  onCancel: (run: Run) => void;
}) {
  const fetch_ = usePluginFetch();
  const [output, setOutput] = useState('');
  const [outputOpen, setOutputOpen] = useState(false);

  useEffect(() => {
    if (outputOpen && !output) {
      api.getOutput(fetch_, run.id).then(setOutput).catch(() => setOutput('(no output available)'));
    }
  }, [outputOpen, output, run.id, fetch_]);

  const fp = run.fingerprint;
  const res = run.result;

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 2 }}>
        <Button size="small" onClick={onBack}>Back</Button>
        <Typography variant="h6" sx={{ flex: 1 }}>
          {run.profile} ({run.driver})
        </Typography>
        <StatusChip status={run.status} />
        {(run.status === 'running' || run.status === 'pending') && (
          <Button size="small" color="warning" variant="outlined" onClick={() => onCancel(run)}>
            Cancel run
          </Button>
        )}
      </Box>

      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        Started {fmtDate(run.startedAt ?? run.createdAt)}
        {run.finishedAt ? `, finished ${fmtDate(run.finishedAt)}` : ''}
        {', '}{run.spec.threads} threads, {run.spec.durationSeconds}s,{' '}
        {run.spec.readPercent}/{run.spec.writePercent} read/write
      </Typography>

      {run.message && run.status === 'failed' && (
        <Alert severity="error" sx={{ mb: 2 }}>{run.message}</Alert>
      )}
      {run.status === 'running' && (
        <Alert severity="info" sx={{ mb: 2 }}>
          Job {run.jobName} is running in namespace {run.namespace}. Results will show up
          here once it finishes.
        </Alert>
      )}

      {res && (
        <>
          <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap', mb: 2 }}>
            <StatTile label="Throughput" value={fmtNum(res.throughputOps)} unit="ops/s" />
            {res.qps ? <StatTile label="Queries" value={fmtNum(res.qps)} unit="q/s" /> : null}
            <StatTile label="Latency avg" value={fmtNum(res.latencyAvgMs, 2)} unit="ms" />
            <StatTile label="Latency p95" value={fmtNum(res.latencyP95Ms, 2)} unit="ms" />
            <StatTile label="Total ops" value={fmtNum(res.totalOps, 0)} />
            <StatTile label="Errors" value={fmtNum(res.errors, 0)} />
          </Box>

          {res.perOperation && Object.keys(res.perOperation).length > 0 && (
            <>
              <Typography variant="subtitle2" sx={{ mb: 1 }}>By operation</Typography>
              <Table size="small" sx={{ mb: 2, maxWidth: 640 }}>
                <TableHead>
                  <TableRow>
                    <TableCell>Operation</TableCell>
                    <TableCell align="right">ops/s</TableCell>
                    <TableCell align="right">count</TableCell>
                    <TableCell align="right">avg (ms)</TableCell>
                    <TableCell align="right">p99 (ms)</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {Object.entries(res.perOperation).map(([op, s]) => (
                    <TableRow key={op}>
                      <TableCell>{op}</TableCell>
                      <TableCell align="right">{fmtNum(s.ops)}</TableCell>
                      <TableCell align="right">{fmtNum(s.count, 0)}</TableCell>
                      <TableCell align="right">{fmtNum(s.avgMs, 2)}</TableCell>
                      <TableCell align="right">{fmtNum(s.p99Ms, 2)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </>
          )}
        </>
      )}

      {fp && (
        <>
          <Divider sx={{ my: 2 }} />
          <Typography variant="subtitle2" sx={{ mb: 1 }}>Environment</Typography>
          {fp.isolated === false && (
            <Alert severity="warning" sx={{ mb: 1 }}>
              The benchmark pod was scheduled on the same node as the database
              ({fp.generatorNode}), so results may be affected by resource contention.
            </Alert>
          )}
          <Table size="small" sx={{ maxWidth: 640, mb: 2 }}>
            <TableBody>
              {[
                ['Engine', `${fp.engine} ${fp.engineVersion ?? ''}`],
                ['Replicas', fp.replicas ? String(fp.replicas) : '—'],
                ['DB resources', [fp.cpuLimit, fp.memoryLimit].filter(Boolean).join(' / ') || '—'],
                ['Storage', [fp.storageClass, fp.storageSize].filter(Boolean).join(', ') || '—'],
                ['Driver image', fp.driverImage],
                ['Generator node', fp.generatorNode ?? '—'],
                ['Database nodes', fp.databaseNodes?.join(', ') ?? '—'],
                ['Isolated', fp.isolated === undefined ? 'unknown' : fp.isolated ? 'yes' : 'no'],
              ].map(([k, v]) => (
                <TableRow key={k}>
                  <TableCell sx={{ color: 'text.secondary', width: 160 }}>{k}</TableCell>
                  <TableCell>{v}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}

      <Accordion expanded={outputOpen} onChange={(_, v) => setOutputOpen(v)} variant="outlined">
        <AccordionSummary>Raw tool output</AccordionSummary>
        <AccordionDetails>
          <Box component="pre" sx={{
            fontSize: 12, whiteSpace: 'pre-wrap', maxHeight: 400, overflow: 'auto',
            bgcolor: 'action.hover', p: 1.5, borderRadius: 1, m: 0,
          }}>
            {output || 'Loading…'}
          </Box>
        </AccordionDetails>
      </Accordion>
    </Box>
  );
}
