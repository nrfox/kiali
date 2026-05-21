import * as React from 'react';
import { render, screen } from '@testing-library/react';
import { EmptyGraphLayout } from '../EmptyGraphLayout';
import { serverConfig, setServerConfig, setPromStatus } from '../../../config/ServerConfig';

describe('EmptyGraphLayout', () => {
  const defaultProps = {
    isError: false,
    isMiniGraph: false,
    loaded: true,
    namespaces: [{ name: 'default' }],
    refreshInterval: 15000,
    showIdleNodes: false,
    toggleIdleNodes: jest.fn()
  };

  afterEach(() => {
    setServerConfig({
      ...serverConfig,
      prometheus: { ...serverConfig.prometheus, enabled: true }
    });
    setPromStatus(undefined);
  });

  it('shows prometheus disabled message when prometheus is explicitly disabled', () => {
    setServerConfig({ ...serverConfig, prometheus: { ...serverConfig.prometheus, enabled: false } });

    const { container } = render(<EmptyGraphLayout {...defaultProps} />);

    expect(container.querySelector('#empty-graph-prometheus-disabled')).toBeInTheDocument();
    expect(screen.getByText('Metrics are disabled')).toBeInTheDocument();
  });

  it('shows prometheus disabled message when prometheus is enabled but unreachable', () => {
    setPromStatus('Prometheus unreachable');

    const { container } = render(<EmptyGraphLayout {...defaultProps} />);

    expect(container.querySelector('#empty-graph-prometheus-disabled')).toBeInTheDocument();
    expect(screen.getByText('Metrics are disabled')).toBeInTheDocument();
  });

  it('does not show prometheus disabled message when prometheus is enabled', () => {
    setServerConfig({ ...serverConfig, prometheus: { ...serverConfig.prometheus, enabled: true } });

    const { container } = render(
      <EmptyGraphLayout {...defaultProps}>
        <div id="graph-content">Graph Content</div>
      </EmptyGraphLayout>
    );

    expect(container.querySelector('#empty-graph-prometheus-disabled')).not.toBeInTheDocument();
  });
});
