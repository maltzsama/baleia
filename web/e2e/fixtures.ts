import AxeBuilder from '@axe-core/playwright';
import { test as base, expect, type Page } from '@playwright/test';

type Fixtures = {
	/** Signs in through the UI with one of the seeded accounts. */
	loginAs: (role: 'admin' | 'member' | 'viewer') => Promise<void>;
	/** Fails on serious or critical axe violations. */
	expectNoA11yViolations: (page: Page) => Promise<void>;
};

export const test = base.extend<Fixtures>({
	loginAs: async ({ page }, use) => {
		await use(async (role) => {
			await page.goto('/login');
			await page.getByPlaceholder('you@company.com').fill(`${role}@e2e.test`);
			await page.getByPlaceholder('••••••••').fill(process.env.E2E_PASSWORD ?? 'e2e-password');
			await page.getByRole('button', { name: /sign in/i }).click();
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
