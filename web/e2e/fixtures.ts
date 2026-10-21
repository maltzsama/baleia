import AxeBuilder from '@axe-core/playwright';
import { test as base, expect, type Page } from '@playwright/test';

type Fixtures = {
	/** Signs in through the UI with one of the seeded accounts. */
	loginAs: (role: 'admin' | 'member' | 'viewer') => Promise<void>;
	/** Fails on serious or critical axe violations. */
	expectNoA11yViolations: (page: Page) => Promise<void>;
};

export const test = base.extend<Fixtures>({
	loginAs: async ({ page }, use, testInfo) => {
		await use(async (role) => {
			await page.goto('/login');
			await page.getByPlaceholder('you@company.com').fill(`${role}@e2e.test`);
			await page.getByPlaceholder('••••••••').fill(process.env.E2E_PASSWORD ?? 'e2e-password');
			// Logins are limited to 10 per minute per IP, and the specs (run by
			// several workers) sign in once each: on 429, wait for Retry-After,
			// give the test that time back, and sign in again (#391).
			for (let attempt = 0; attempt < 15; attempt++) {
				const response = page.waitForResponse(
					(res) => res.url().endsWith('/api/v1/auth/login') && res.request().method() === 'POST'
				);
				await page.getByRole('button', { name: /sign in/i }).click();
				const login = await response;
				if (login.status() !== 429) break;
				const waitMs = (Number(login.headers()['retry-after'] ?? '6') + 1) * 1000;
				testInfo.setTimeout(testInfo.timeout + waitMs);
				await page.waitForTimeout(waitMs);
			}
			await page.waitForURL('**/sql');
		});
	},
	expectNoA11yViolations: async ({ page }, use) => {
		await use(async (target) => {
			const results = await new AxeBuilder({ page: target ?? page }).analyze();
			const serious = results.violations.filter(
				(violation) => violation.impact === 'serious' || violation.impact === 'critical'
			);
			expect(serious, JSON.stringify(serious.map((violation) => violation.id))).toEqual([]);
		});
	}
});

export { expect };
