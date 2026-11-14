import { describe, expect, it } from 'vitest';
import { load } from './+page';

// #1019: the glossary moved next to the models; the old address still works.
describe('settings/search', () => {
	it('redirects to the glossary under the models', () => {
		let thrown: unknown;
		try {
			load({} as Parameters<typeof load>[0]);
		} catch (e) {
			thrown = e;
		}

		expect(thrown).toMatchObject({ status: 308, location: '/models/glossary' });
	});
});
