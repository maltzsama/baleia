import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { makeBIGatewayStatus, makeWorkspaceBIStatus } from '../../test/factories';
import SetupGuide from './SetupGuide.svelte';

const workspaceHref = '/settings/workspaces/00000000-0000-0000-0000-000000000001?tab=bi';

function currentStep(): HTMLElement | null {
	return document.querySelector('[aria-current="step"]');
}

describe('BI gateway setup guide (#1098)', () => {
	it('points at the operator step while the gateway is not configured', () => {
		render(SetupGuide, {
			props: {
				gateway: makeBIGatewayStatus({
					configured: false,
					enabled: false,
					public_url_ready: false,
					public_url: null,
					state: 'blocked',
					blocked_by: ['public_url_not_configured']
				}),
				workspaceHref
			}
		});

		expect(screen.getByText('Setup guide')).toBeTruthy();
		expect(currentStep()?.textContent).toContain('Configure the gateway');
		expect(screen.getByText(/Still missing:/)).toBeTruthy();
		expect(screen.getByText(/public URL is not configured/)).toBeTruthy();
	});

	it('points at the global switch once the gateway is configured', () => {
		render(SetupGuide, {
			props: {
				gateway: makeBIGatewayStatus({ enabled: false, state: 'disabled' }),
				workspaceHref
			}
		});

		expect(currentStep()?.textContent).toContain('Enable the gateway');
	});

	it('points at the workspace switch once the gateway is ready', () => {
		render(SetupGuide, {
			props: { gateway: makeBIGatewayStatus(), workspaceHref }
		});

		expect(currentStep()?.textContent).toContain('Enable BI for the workspace');
	});

	it('asks a workspace admin when the caller cannot enable BI', () => {
		render(SetupGuide, {
			props: {
				gateway: makeBIGatewayStatus(),
				workspace: makeWorkspaceBIStatus({ workspace_bi_enabled: false }),
				workspaceHref,
				canManageWorkspace: false
			}
		});

		expect(currentStep()?.textContent).toContain('Enable BI for the workspace');
		expect(
			screen.getByText('Ask a workspace administrator to enable BI for this workspace.')
		).toBeTruthy();
	});

	it('finishes at the client connection and states the protocol', () => {
		render(SetupGuide, {
			props: {
				gateway: makeBIGatewayStatus(),
				workspace: makeWorkspaceBIStatus(),
				hasKeys: true,
				workspaceHref
			}
		});

		expect(currentStep()?.textContent).toContain('Connect Tableau or Power BI');
		expect(screen.getByText(/not an Arrow Flight SQL endpoint/)).toBeTruthy();
	});
});
