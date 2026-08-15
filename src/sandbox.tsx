// Dev sandbox: a mock OpenEverest host for `npm run dev`. It lists the
// databases the backend knows about (Everest-managed + static) in a picker,
// then renders the Performance tab for the selection — standing in for the
// database-detail page of the real dashboard. Production loading goes
// through register() in main.tsx instead.
import { useEffect, useState } from 'react';
import ReactDOM from 'react-dom/client';
import Box from '@mui/material/Box';
import Alert from '@mui/material/Alert';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import PerformanceTab from './PerformanceTab';
import type { Instance, PluginFetch } from './types';

const sandboxFetch: PluginFetch = (path, init) => fetch(path, init);

function Sandbox() {
  const params = new URLSearchParams(window.location.search);
  const [instances, setInstances] = useState<Instance[] | null>(null);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState(() => {
    const ns = params.get('namespace');
    const name = params.get('instance');
    return ns && name ? `${ns}/${name}` : '';
  });

  useEffect(() => {
    fetch('/api/instances')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`${r.status}`))))
      .then((data) => {
        const list: Instance[] = data.instances ?? [];
        setInstances(list);
        if (!selected && list.length > 0) {
          setSelected(`${list[0].namespace}/${list[0].name}`);
        }
      })
      .catch(() => setError(
        'Cannot reach the plugin backend on 127.0.0.1:8081 — start it with: source scripts/demo-env.sh && ./backend/plugin-backend',
      ));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (error) return <Alert severity="error" sx={{ m: 2 }}>{error}</Alert>;
  if (instances === null) return <Typography sx={{ m: 2 }}>Loading databases…</Typography>;
  if (instances.length === 0 && !selected) {
    return (
      <Alert severity="warning" sx={{ m: 2 }}>
        The backend knows no databases. Register demo databases via
        STATIC_INSTANCES (see scripts/demo-env.sh) or run against a cluster
        with Everest DatabaseCluster CRs.
      </Alert>
    );
  }

  const [namespace, instanceName] = selected.split('/');
  const inst = instances.find((i) => i.namespace === namespace && i.name === instanceName);

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, px: 2, pt: 2 }}>
        <TextField
          select size="small" label="Database" value={selected}
          onChange={(e) => setSelected(e.target.value)}
          sx={{ minWidth: 320 }}
        >
          {instances.map((i) => (
            <MenuItem key={`${i.namespace}/${i.name}`} value={`${i.namespace}/${i.name}`}>
              {i.namespace}/{i.name} — {i.engine}{i.status ? ` (${i.status})` : ''}
            </MenuItem>
          ))}
        </TextField>
        <Typography variant="caption" color="text.secondary">
          Stand-in for the database-detail page; in OpenEverest this tab appears per database.
        </Typography>
      </Box>
      {namespace && instanceName && (
        <PerformanceTab
          key={selected}
          namespace={namespace}
          instanceName={instanceName}
          engine={inst?.engine}
          pluginFetch={sandboxFetch}
        />
      )}
    </Box>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(<Sandbox />);
