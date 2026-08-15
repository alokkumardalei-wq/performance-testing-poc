import { useEffect, useMemo, useState } from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardActionArea from '@mui/material/CardActionArea';
import CardContent from '@mui/material/CardContent';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import FormControlLabel from '@mui/material/FormControlLabel';
import MenuItem from '@mui/material/MenuItem';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { api, usePluginFetch } from './api';
import type { DriverInfo, Engine, Profile, Run } from './types';

export function NewRunDialog({ open, onClose, onCreated, namespace, instanceName, engine }: {
  open: boolean;
  onClose: () => void;
  onCreated: (run: Run) => void;
  namespace: string;
  instanceName: string;
  engine: Engine;
}) {
  const fetch_ = usePluginFetch();
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [drivers, setDrivers] = useState<DriverInfo[]>([]);
  const [profileName, setProfileName] = useState('smoke');
  const [driverName, setDriverName] = useState('');
  const [threads, setThreads] = useState<string>('');
  const [duration, setDuration] = useState<string>('');
  const [skipPrepare, setSkipPrepare] = useState(false);
  const [skipCleanup, setSkipCleanup] = useState(false);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) return;
    api.profiles(fetch_).then(setProfiles).catch((e) => setError(String(e.message ?? e)));
    api.drivers(fetch_).then(setDrivers).catch(() => { /* driver list is optional */ });
  }, [open, fetch_]);

  const compatibleDrivers = useMemo(
    () => drivers.filter((d) => d.engines.includes(engine)),
    [drivers, engine],
  );
  const selected = profiles.find((p) => p.name === profileName);

  const submit = async () => {
    setSubmitting(true);
    setError('');
    try {
      const overrides: Record<string, unknown> = {};
      if (threads) overrides.threads = Number(threads);
      if (duration) overrides.durationSeconds = Number(duration);
      if (skipPrepare) overrides.skipPrepare = true;
      if (skipCleanup) overrides.skipCleanup = true;
      const run = await api.createRun(fetch_, {
        namespace,
        instanceName,
        profile: profileName,
        driver: driverName || undefined,
        overrides,
      });
      onCreated(run);
      onClose();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth>
      <DialogTitle>New benchmark run — {instanceName}</DialogTitle>
      <DialogContent>
        {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}
        <Typography variant="subtitle2" sx={{ mb: 1 }}>Workload profile</Typography>
        <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 1.5, mb: 3 }}>
          {profiles.map((p) => (
            <Card
              key={p.name}
              variant="outlined"
              sx={{ borderColor: p.name === profileName ? 'primary.main' : undefined, borderWidth: p.name === profileName ? 2 : 1 }}
            >
              <CardActionArea onClick={() => setProfileName(p.name)} sx={{ height: '100%' }}>
                <CardContent>
                  <Typography variant="subtitle2">{p.displayName}</Typography>
                  <Typography variant="caption" color="text.secondary">{p.description}</Typography>
                </CardContent>
              </CardActionArea>
            </Card>
          ))}
        </Box>

        <Typography variant="subtitle2" sx={{ mb: 1 }}>Options</Typography>
        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap', alignItems: 'center' }}>
          <TextField
            select size="small" label="Driver" value={driverName}
            onChange={(e) => setDriverName(e.target.value)}
            sx={{ minWidth: 180 }}
            helperText="Auto picks the best driver"
          >
            <MenuItem value="">Auto</MenuItem>
            {compatibleDrivers.map((d) => <MenuItem key={d.name} value={d.name}>{d.name}</MenuItem>)}
          </TextField>
          <TextField
            size="small" label="Threads" type="number" value={threads}
            onChange={(e) => setThreads(e.target.value)}
            placeholder={selected ? String(selected.spec.threads) : ''}
            sx={{ width: 120 }}
          />
          <TextField
            size="small" label="Duration (s)" type="number" value={duration}
            onChange={(e) => setDuration(e.target.value)}
            placeholder={selected ? String(selected.spec.durationSeconds) : ''}
            sx={{ width: 140 }}
          />
          <FormControlLabel
            control={<Switch checked={skipPrepare} onChange={(e) => setSkipPrepare(e.target.checked)} />}
            label="Reuse existing dataset"
          />
          <FormControlLabel
            control={<Switch checked={skipCleanup} onChange={(e) => setSkipCleanup(e.target.checked)} />}
            label="Keep dataset after run"
          />
        </Box>
        {selected && (
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 2 }}>
            {selected.spec.readPercent}% reads / {selected.spec.writePercent}% writes ·{' '}
            {threads || selected.spec.threads} threads · {duration || selected.spec.durationSeconds}s
            {engine === 'mongodb'
              ? ` · ${selected.spec.records.toLocaleString()} documents`
              : ` · ${selected.spec.tables} tables × ${selected.spec.tableSize.toLocaleString()} rows`}
          </Typography>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" onClick={submit} disabled={submitting || !profiles.length}>
          {submitting ? 'Starting…' : 'Start run'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
