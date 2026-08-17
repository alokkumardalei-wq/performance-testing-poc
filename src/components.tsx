import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Paper from '@mui/material/Paper';
import Typography from '@mui/material/Typography';
import CircularProgress from '@mui/material/CircularProgress';
import type { RunStatus } from './types';

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
      variant="outlined"
      label={status}
      color={statusColor[status]}
      icon={status === 'running' ? <CircularProgress size={11} color="inherit" /> : undefined}
      sx={{ textTransform: 'capitalize', height: 22 }}
    />
  );
}

export function StatTile({ label, value, unit, hint }: {
  label: string; value: string; unit?: string; hint?: string;
}) {
  return (
    <Paper variant="outlined" sx={{ px: 2, py: 1.25, minWidth: 150 }}>
      <Typography variant="caption" color="text.secondary">{label}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
        <Typography component="div" sx={{ fontSize: 19, fontWeight: 600, lineHeight: 1.3, fontVariantNumeric: 'tabular-nums' }}>{value}</Typography>
        {unit && <Typography sx={{ fontSize: 12, color: 'text.secondary' }}>{unit}</Typography>}
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
