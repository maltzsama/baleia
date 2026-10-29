<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { API_ROOT, client } from '$lib/api/client';
	import { bootstrap, login as loginSession, safeNext } from '$lib/auth/session';
	import { describeError } from '$lib/errors/catalog';
	import { Input, PasswordInput, Select } from '$lib/ui';
	import Logo from '$lib/components/Logo.svelte';

	type Provider = { id: string; name: string; type: 'oidc' | 'ldap' };

	let dark = $state(false);
	let screen = $state<'login' | 'forgot' | 'sent'>('login');

	let email = $state('');
	let password = $state('');
	let error = $state('');
	let notice = $state('');
	let shakeField = $state('');
	let busy = $state(false);
	let checking = $state(false);
	let retryIn = $state(0);
	let providers = $state<Provider[]>([]);
	// 'local' or the id of the selected LDAP provider.
	let authMode = $state('local');

	const oidcProviders = $derived(providers.filter((p) => p.type === 'oidc'));
	const ldapProviders = $derived(providers.filter((p) => p.type === 'ldap'));
	const ldapProvider = $derived(ldapProviders.find((p) => p.id === authMode) ?? null);
	const signInOptions = $derived([
		{ value: 'local', label: m.login_email_and_password() },
		...ldapProviders.map((p) => ({ value: p.id, label: p.name }))
	]);

	let retryTimer: ReturnType<typeof setInterval> | undefined;
	let slowTimer: ReturnType<typeof setTimeout> | undefined;

	function credentialError(code: string): string {
		const described = describeError(code);
		return `${described.title}: ${described.explanation}`;
	}

	onMount(() => {
		dark = document.body.classList.contains('dark');

		// Shown once after /reset-password or an SSO failure, then dropped from
		// the address bar (the `next` parameter is preserved).
		const params = new SvelteURLSearchParams(window.location.search);
		let cleanUrl = false;
		if (params.get('reset') === '1') {
			notice = m.login_password_changed_sign_in_with_your_new_pas();
			params.delete('reset');
			cleanUrl = true;
		}
		const queryError = params.get('error');
		if (queryError) {
			error = credentialError(queryError);
			params.delete('error');
			cleanUrl = true;
		}
		if (cleanUrl) {
			const query = params.toString();
			history.replaceState(null, '', `/login${query ? `?${query}` : ''}`);
		}

		loadProviders();

		// Restore a session from the HttpOnly refresh cookie. Without a valid
		// cookie the form stays usable instead of bouncing /login → /sql → /login.
		const next = new URLSearchParams(window.location.search).get('next');
		bootstrap().then((state) => {
			if (state === 'authenticated') window.location.href = safeNext(next);
		});

		return () => {
			clearInterval(retryTimer);
			clearTimeout(slowTimer);
		};
	});

	async function loadProviders() {
		try {
			const { data } = await client.GET('/auth/providers');
			providers = (data as Provider[] | undefined) ?? [];
		} catch {
			// No providers means no SSO/LDAP section; password login still works.
		}
	}

	function showError(msg: string, field = '') {
		error = msg;
		if (field) {
			shakeField = field;
			setTimeout(() => (shakeField = ''), 1000);
		}
	}

	function startRetryCountdown(seconds: number) {
		clearInterval(retryTimer);
		retryIn = seconds;
		const title = describeError('rate_limited').title;
		error = m.login_try_again_in_s({ title, retryIn });
		retryTimer = setInterval(() => {
			retryIn -= 1;
			if (retryIn <= 0) {
				clearInterval(retryTimer);
				retryIn = 0;
				error = '';
			} else {
				error = m.login_try_again_in_s({ title, retryIn });
			}
		}, 1000);
	}

	async function doLogin() {
		if (busy || retryIn > 0) return;
		if (!email || !password) {
			showError(m.login_please_fill_all_fields(), 'password');
			return;
		}
		busy = true;
		checking = false;
		error = '';
		notice = '';
		// The progressive delay can hold the response for up to 60 s; tell the
		// user the request is still working after one second (#290).
		slowTimer = setTimeout(() => (checking = true), 1000);
		try {
			// With an LDAP provider selected the first field is the directory
			// username and the body carries provider_id, not email (#289).
			const result = ldapProvider
				? await loginSession({
						provider_id: ldapProvider.id,
						username: email.trim(),
						password
					})
				: await loginSession({ email, password });
			if (!result.ok) {
				if (result.code === 'rate_limited') {
					if (result.retryAfter && result.retryAfter > 0) startRetryCountdown(result.retryAfter);
					else showError(credentialError('rate_limited'));
					return;
				}
				if (result.code === 'network_error') {
					showError(credentialError('network_error'));
					return;
				}
				if (
					result.code === 'invalid_credentials' ||
					result.code === 'account_suspended' ||
					result.code === 'not_provisioned' ||
					result.code === 'idp_unavailable' ||
					result.code === 'sso_failed'
				) {
					showError(
						credentialError(result.code),
						result.code === 'invalid_credentials' ? 'password' : ''
					);
					return;
				}
				showError(credentialError('internal'));
				return;
			}
			const next = new URLSearchParams(window.location.search).get('next');
			window.location.href = safeNext(next);
		} finally {
			clearTimeout(slowTimer);
			checking = false;
			busy = false;
		}
	}

	async function sendReset() {
		if (busy || !email) return;
		busy = true;
		error = '';
		try {
			const { response } = await client.POST('/auth/password-reset/request', {
				body: { email }
			});
			if (response.status === 204) {
				// Same screen whether or not the account exists.
				screen = 'sent';
			} else if (response.status === 503) {
				showError(m.login_password_reset_by_e_mail_is_not_configured());
			} else {
				showError(m.login_could_not_request_a_reset_link({ status: response.status }));
			}
		} catch {
			showError(m.setup_connection_error_is_the_server_running());
		} finally {
			busy = false;
		}
	}

	function doSSO(providerId: string) {
		const target = safeNext(new URLSearchParams(window.location.search).get('next'), '');
		const suffix = target ? `?next=${encodeURIComponent(target)}` : '';
		window.location.href = `${API_ROOT}/auth/oidc/${encodeURIComponent(providerId)}/login${suffix}`;
	}

	function go(s: typeof screen) {
		error = '';
		screen = s;
	}

	function toggleDark() {
		dark = !dark;
		// Same mechanism as the app layout: class on <body>, persisted under `theme`.
		document.body.classList.toggle('dark', dark);
		try {
			localStorage.setItem('theme', dark ? 'dark' : 'light');
		} catch {
			/* storage unavailable */
		}
	}
</script>

<div
	class="bg-bg-app text-text-main min-h-screen flex overflow-hidden transition-colors duration-200"
>
	<!-- Left branding -->
	<div
		class="flex-1 bg-brand-dark flex flex-col items-center justify-center p-12 relative overflow-hidden
		before:content-[''] before:absolute before:inset-0
		before:[background-image:linear-gradient(rgba(59,130,246,.06)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,.06)_1px,transparent_1px)]
		before:bg-[length:40px_40px]
		after:content-[''] after:absolute after:w-80 after:h-80 after:rounded-full
		after:[background:radial-gradient(circle,rgba(59,130,246,.18)_0%,transparent_70%)]
		after:top-1/2 after:left-1/2 after:-translate-x-1/2 after:-translate-y-1/2 after:pointer-events-none
		max-[680px]:hidden"
	>
		<div class="relative z-[1] flex flex-col items-center text-center gap-6">
			<Logo class="h-[96px] w-[96px]" />
			<div class="text-[32px] font-bold tracking-[-1px] text-slate-100">{m.common_baleia()}</div>
			<div class="text-[13px] text-slate-500 leading-[1.6] max-w-[260px]">
				{m.login_a_powerful_sql_client_for_your_self_hosted()}
			</div>
			<div
				class="font-mono text-[11px] text-slate-700 bg-white/[.04] border border-slate-800 py-1 px-3 rounded-[20px]"
			>
				{m.login_v1_0_0_beta()}
			</div>
		</div>
	</div>

	<!-- Right form -->
	<div
		class="w-[420px] shrink-0 flex flex-col items-center justify-center py-10 px-8 overflow-y-auto relative"
	>
		<div class="absolute top-4 right-5 flex items-center gap-1.5 text-[11px] text-text-muted">
			<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"
				><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" /></svg
			>
			<!-- A native switch button (#484): named, Enter and Space both toggle. -->
			<button
				type="button"
				role="switch"
				aria-checked={dark}
				aria-label={m.login_dark_theme()}
				onclick={toggleDark}
				class="w-8 h-[18px] rounded-full cursor-pointer relative border-none p-0 transition-colors duration-200
					{dark ? 'bg-primary' : 'bg-border'}"
			>
				<span
					class="absolute w-[14px] h-[14px] bg-white rounded-full top-[2px] left-[2px] shadow-sm transition-transform duration-200
					{dark ? 'translate-x-[14px]' : 'translate-x-0'}"
				></span>
			</button>
		</div>

		<div class="w-full max-w-[360px]">
			{#if screen === 'login'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">{m.login_sign_in()}</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						{m.login_welcome_back_enter_your_credentials_to_con()}
					</div>
					{#if notice}
						<div
							role="status"
							class="bg-green-50 border border-green-200 rounded-md py-[9px] px-3 text-xs text-green-700 mb-3.5"
						>
							{notice}
						</div>
					{/if}
					{#if error}
						<div
							role="alert"
							class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5"
						>
							{error}
						</div>
					{/if}
					{#if ldapProviders.length > 0}
						<div class="mb-3.5">
							<Select
								label={m.login_sign_in_with()}
								bind:value={authMode}
								options={signInOptions}
							/>
						</div>
					{/if}
					<div class="mb-3.5">
						<Input
							label={ldapProvider ? m.login_username() : m.common_email()}
							type={ldapProvider ? 'text' : 'email'}
							autocomplete={ldapProvider ? 'username' : 'email'}
							bind:value={email}
							placeholder={ldapProvider ? 'alice' : 'you@company.com'}
							onkeydown={(e) => e.key === 'Enter' && doLogin()}
						/>
					</div>
					<div class="mb-3.5">
						<PasswordInput
							label={m.common_password()}
							autocomplete="current-password"
							bind:value={password}
							placeholder="••••••••"
							class={shakeField === 'password' ? 'animate-[shake_.35s_ease]' : ''}
							onkeydown={(e) => e.key === 'Enter' && doLogin()}
						/>
					</div>
					{#if !ldapProvider}
						<div class="text-right text-[11px] -mt-2 mb-4">
							<button
								type="button"
								class="cursor-pointer border-none bg-transparent p-0 text-[11px] font-semibold text-primary hover:text-primary-hover"
								onclick={() => go('forgot')}>{m.login_forgot_password()}</button
							>
						</div>
					{:else}
						<div class="mb-4"></div>
					{/if}
					<button
						onclick={doLogin}
						disabled={busy || retryIn > 0}
						class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5
							hover:enabled:bg-primary-hover disabled:bg-blue-300 disabled:cursor-not-allowed"
					>
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4" /><polyline
								points="10 17 15 12 10 7"
							/><line x1="15" y1="12" x2="3" y2="12" /></svg
						>
						{busy
							? checking
								? m.login_checking_credentials()
								: m.login_signing_in()
							: m.login_sign_in()}
					</button>
					{#if oidcProviders.length > 0}
						<div
							class="flex items-center gap-2.5 my-4 text-[11px] font-semibold text-text-muted
							before:content-[''] before:flex-1 before:h-px before:bg-border
							after:content-[''] after:flex-1 after:h-px after:bg-border"
						>
							{m.login_or()}
						</div>
						<div class="flex flex-col gap-2">
							{#each oidcProviders as p (p.id)}
								<button
									onclick={() => doSSO(p.id)}
									class="w-full h-10 bg-transparent border border-border rounded-[7px] text-[13px] font-medium text-text-main cursor-pointer transition-all duration-150 flex items-center justify-center gap-[9px] hover:border-border-hover hover:bg-bg-app"
								>
									{m.login_continue_with()}
									{p.name}
								</button>
							{/each}
						</div>
					{/if}
				</div>
			{/if}

			{#if screen === 'forgot'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<button
						type="button"
						class="flex items-center gap-[5px] text-xs font-semibold text-text-muted cursor-pointer mb-5 w-fit border-none bg-transparent p-0 transition-colors duration-150 hover:text-text-main"
						onclick={() => go('login')}
					>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"
							><polyline points="15 18 9 12 15 6" /></svg
						>
						{m.login_back_to_sign_in()}
					</button>
					<div class="text-xl font-bold tracking-[-.4px] mb-1">{m.login_forgot_password()}</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						{m.login_enter_your_email_and_we_ll_send_a_link_to()}
					</div>
					{#if error}
						<div
							role="alert"
							class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5"
						>
							{error}
						</div>
					{/if}
					<div class="mb-3.5">
						<Input
							label={m.common_email()}
							type="email"
							autocomplete="email"
							bind:value={email}
							placeholder={m.login_you_company_com()}
							onkeydown={(e) => e.key === 'Enter' && sendReset()}
						/>
					</div>
					<button
						onclick={sendReset}
						disabled={busy || !email}
						class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5
							hover:enabled:bg-primary-hover disabled:bg-blue-300 disabled:cursor-not-allowed"
					>
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"
							><line x1="22" y1="2" x2="11" y2="13" /><polygon
								points="22 2 15 22 11 13 2 9 22 2"
							/></svg
						>
						{busy ? m.login_sending() : m.login_send_reset_link()}
					</button>
				</div>
			{/if}

			{#if screen === 'sent'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]" role="status">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">{m.login_check_your_email()}</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						{m.login_if_an_account_exists_for_this_address_we_s()}
					</div>
					<button
						onclick={() => go('login')}
						class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover"
					>
						{m.login_back_to_sign_in()}
					</button>
				</div>
			{/if}
		</div>

		<a
			href={resolve('/credits')}
			class="absolute bottom-4 text-[11px] text-text-muted no-underline transition-colors duration-150 hover:text-text-main"
		>
			{m.common_credits()}
		</a>
	</div>
</div>

<style>
	@keyframes fadeIn {
		from {
			opacity: 0;
			transform: translateY(6px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
	@keyframes shake {
		0%,
		100% {
			transform: translateX(0);
		}
		20%,
		60% {
			transform: translateX(-5px);
		}
		40%,
		80% {
			transform: translateX(5px);
		}
	}
</style>
