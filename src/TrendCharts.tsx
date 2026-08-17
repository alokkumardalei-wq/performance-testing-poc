import Box from '@mui/material/Box';
import Paper from '@mui/material/Paper';
import Typography from '@mui/material/Typography';
import { LineChart } from '@mui/x-charts/LineChart';
import type { Run } from './types';

const BLUE = '#2a78d6';
const ORANGE = '#eb6834';

interface Point {
  label: string;
  y: number;
}

function TrendChart({ title, context, unit, color, points }: {
  title: string; context: string; unit: string; color: string; points: Point[];
}) {
  return (
    <Paper variant="outlined" sx={{ p: 2, flex: 1, minWidth: 320 }}>
      <Typography variant="subtitle2">
        {title}{' '}
        <Typography component="span" variant="caption" color="text.secondary">
          ({unit}), {context}
        </Typography>
      </Typography>
      <LineChart
        height={220}
        series={[{
          data: points.map((p) => p.y),
          color,
          showMark: true,
          curve: 'linear',
          valueFormatter: (v) => (v == null ? '' : `${v.toLocaleString(undefined, { maximumFractionDigits: 1 })} ${unit}`),
        }]}
        xAxis={[{ data: points.map((p) => p.label), scaleType: 'point' }]}
        grid={{ horizontal: true }}
        hideLegend
        sx={{ '& .MuiLineElement-root': { strokeWidth: 2 } }}
      />
    </Paper>
  );
}

// Only chart runs whose config matches the most recent succeeded run, so the
// points on a trend line are actually comparable.
export function TrendCharts({ runs }: { runs: Run[] }) {
  const done = runs
    .filter((r) => r.status === 'succeeded' && r.result)
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  if (done.length < 2) return null;

  const anchor = done[done.length - 1];
  const matching = done.filter((r) =>
    r.profile === anchor.profile &&
    r.driver === anchor.driver &&
    r.spec.threads === anchor.spec.threads &&
    r.spec.durationSeconds === anchor.spec.durationSeconds);
  if (matching.length < 2) return null;

  const context = `${anchor.profile}, ${anchor.driver}, ${anchor.spec.threads} threads`;
  // Add seconds to x labels only when two runs share a minute.
  const minutes = matching.map((r) =>
    new Date(r.createdAt).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }));
  const needSeconds = new Set(minutes).size < minutes.length;
  const label = (r: Run) =>
    new Date(r.createdAt).toLocaleTimeString(undefined, {
      hour: '2-digit', minute: '2-digit', ...(needSeconds ? { second: '2-digit' } : {}),
    });

  return (
    <Box sx={{ mb: 2 }}>
      <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
        <TrendChart
          title="Throughput"
          context={context}
          unit="ops/s"
          color={BLUE}
          points={matching.map((r) => ({ label: label(r), y: r.result!.throughputOps }))}
        />
        <TrendChart
          title="Latency p95"
          context={context}
          unit="ms"
          color={ORANGE}
          points={matching.map((r) => ({ label: label(r), y: r.result!.latencyP95Ms }))}
        />
      </Box>
      {matching.length < done.length && (
        <Typography variant="caption" color="text.secondary">
          Showing the {matching.length} runs with the same settings as the latest run
          ({context}). Runs with other settings are left out of the trend.
        </Typography>
      )}
    </Box>
  );
}
