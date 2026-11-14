import { afterEach, describe, expect, it } from 'vitest';
import { setAppLocale } from '$lib/i18n/locale.svelte';
import { locate, navAreas, sectionTitle, visiblePages } from './sections';

// #465: the header names the section of every route, in both locales.
describe('nav sections', () => {
	afterEach(() => setAppLocale('en'));

	it.each([
		['/sql', 'SQL · Editor', 'SQL · Editor'],
		['/sql/q/abc', 'SQL · Editor', 'SQL · Editor'],
		['/history', 'SQL · History', 'SQL · Histórico'],
		['/data', 'Data · Explorer', 'Dados · Explorador'],
		['/data/docs', 'Data · Docs', 'Dados · Documentação'],
		['/explore', 'Explore', 'Explorar'],
		['/models/m1', 'Explore · Models', 'Explorar · Modelos'],
		['/content/spaces/s1', 'Content · Spaces', 'Conteúdo · Espaços'],
		['/content/trash', 'Content · Trash', 'Conteúdo · Lixeira'],
		['/metrics/new', 'Content · Metrics', 'Conteúdo · Métricas'],
		['/connections/new', 'Connections', 'Conexões'],
		['/catalogs/c1', 'Connections · Catalogs', 'Conexões · Catálogos'],
		['/engines', 'Connections · Engines', 'Conexões · Engines'],
		['/monitoring', 'Monitoring', 'Monitoramento'],
		['/settings', 'Settings', 'Configurações'],
		['/settings/users', 'Settings · Users', 'Configurações · Usuários'],
		['/profile', 'Profile', 'Perfil'],
		['/profile/api-keys', 'Profile · API keys', 'Perfil · Chaves de API']
	])('%s is "%s" (en) and "%s" (pt-BR)', (path, en, pt) => {
		setAppLocale('en');
		expect(sectionTitle(path)).toBe(en);
		setAppLocale('pt-BR');
		expect(sectionTitle(path)).toBe(pt);
	});

	it('takes the longest matching page', () => {
		expect(locate('/data/docs')?.page?.href).toBe('/data/docs');
		expect(locate('/content/trash')?.page?.href).toBe('/content/trash');
		expect(locate('/content')?.page?.href).toBe('/content');
		expect(locate('/sql/anything')?.page?.href).toBe('/sql');
		expect(locate('/database')).toBeNull();
	});

	it('returns the area title for an unknown descendant route', () => {
		expect(sectionTitle('/connections/new')).toBe('Connections');
	});

	it('hides a page behind its feature flag', () => {
		const content = navAreas.find((area) => area.key === 'content')!;
		const hrefs = (on: boolean) =>
			visiblePages(content, {
				agents: false,
				assistant: false,
				bi_gateway: false,
				retrievers: on
			}).map((page) => page.href);
		expect(hrefs(true)).toContain('/retrievers');
		expect(hrefs(false)).not.toContain('/retrievers');
	});

	it('does not title a hidden feature-gated page when features are supplied', () => {
		expect(
			sectionTitle('/retrievers', {
				agents: false,
				assistant: false,
				bi_gateway: false,
				retrievers: false
			})
		).toBe('Content');
	});
});
