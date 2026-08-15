import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Paper from '@mui/material/Paper';
import Typography from '@mui/material/Typography';
import CircularProgress from '@mui/material/CircularProgress';
import type { RunStatus } from './types';

// Status → MUI semantic color. Status colors are reserved for state and are
// not reused as chart series colors.
const statusColor: Record<RunStatus, 'default' | 'info' | 'success' | 'error' | 'warning'> = {
  pending: 'default',
  running: 'info',
  succeeded: 'success',
  failed: 'error',
  canceled: 'warning',
};

export function StatusChip({ status }: { status: RunStatus }) {
  return (
    <Chip
      size="small"
      label={status}
      color={statusColor[status]}
      icon={status === 'running' ? <CircularProgress size={12} color="inherit" /> : undefined}
      sx={{ textTransform: 'capitalize' }}
    />
  );
}

// StatTile: a headline number is a tile, not a chart.
export function StatTile({ label, value, unit, hint }: {
  label: string; value: string; unit?: string; hint?: string;
}) {
  return (
    <Paper variant="outlined" sx={{ p: 2, minWidth: 150, flex: 1 }}>
      <Typography variant="caption" color="text.secondary" sx={{ textTransform: 'uppercase', letterSpacing: 0.5 }}>
        {label}
      </Typography>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
        <Typography variant="h5" component="div" sx={{ fontWeight: 600 }}>{value}</Typography>
        {unit && <Typography variant="body2" color="text.secondary">{unit}</Typography>}
      </Box>
      {hint && <Typography variant="caption" color="text.secondary">{hint}</Typography>}
    </Paper>
  );
}

export function fmtNum(n: number | undefined, digits = 1): string {
  if (n === undefined || Number.isNaN(n)) return '—';
  if (n >= 10000) return n.toLocaleString(undefined, { maximumFractionDigits: 0 });
  return n.toLocaleString(undefined, { maximumFractionDigits: digits });
}

export function fmtDate(iso: string | undefined): string {
  if (!iso) return '—';
  return new Date(iso).toLocaleString();
}
