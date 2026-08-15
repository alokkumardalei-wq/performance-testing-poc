// Plugin entry point: the OpenEverest host dynamic-import()s this bundle and
// calls register(api). See docs/architecture.md for the loading model.
//
// Micro-frontend pattern: the component handed to the host is a thin wrapper
// built with the HOST's React (api.React) that renders a bare <div>; the
// plugin mounts its own React tree (bundled React + MUI) inside it. This
// avoids the dual-React hooks crash without constraining the host's or the
// plugin's dependency versions.
import ReactLocal from 'react';
import ReactDOMLocal from 'react-dom/client';
import PerformanceTab from './PerformanceTab';
import type { PluginFetch } from './types';

// Minimal structural types for the host API (mirrors @openeverest/plugin-sdk;
// kept inline so the bundle has zero runtime deps on the SDK package).
interface PluginApi {
  React: typeof ReactLocal;
  registerExtension(ext: unknown): void;
  fetch: PluginFetch;
}

interface ClusterDetailTabProps {
  cluster?: { metadata?: { name?: string; namespace?: string } };
  namespace?: string;
  instanceName?: string;
}

function register(api: PluginApi) {
  const pluginFetch: PluginFetch = api.fetch.bind(api);

  const MicroFrontendWrapper = (props: ClusterDetailTabProps) => {
    const divRef = api.React.useRef<HTMLDivElement>(null);
    const rootRef = api.React.useRef<ReturnType<typeof ReactDOMLocal.createRoot> | null>(null);

    api.React.useEffect(() => {
      if (!divRef.current) return;
      if (!rootRef.current) {
        rootRef.current = ReactDOMLocal.createRoot(divRef.current);
      }
      const namespace = props.namespace ?? props.cluster?.metadata?.namespace ?? 'default';
      const instanceName = props.instanceName ?? props.cluster?.metadata?.name ?? '';
      rootRef.current.render(
        ReactLocal.createElement(PerformanceTab, { namespace, instanceName, pluginFetch }),
      );
    }, [props]);

    api.React.useEffect(() => {
      return () => {
        // Deferred unmount dodges React's "cannot unmount during render".
        setTimeout(() => rootRef.current?.unmount(), 0);
      };
    }, []);

    return api.React.createElement('div', {
      ref: divRef,
      style: { width: '100%', height: '100%' },
    });
  };

  api.registerExtension({
    type: 'clusterDetailTab',
    label: 'Performance',
    path: 'performance',
    // Note: `providers` deliberately omitted — the documented values do not
    // match what the v2 filter compares against (see docs/architecture.md);
    // engine compatibility is handled inside the component instead.
    component: MicroFrontendWrapper,
  });
}

export { register };
export default register;
