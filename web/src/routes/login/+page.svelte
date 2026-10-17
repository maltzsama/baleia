<script lang="ts">
	import { onMount } from 'svelte';
	import { API_ROOT, clearSession, client, setAccessToken, setRefreshToken } from '$lib/api/client';

	// Retry-After is either a number of seconds or an HTTP-date. Returns the
	// remaining delay in whole seconds, capped at one hour; 0 when absent,
	// malformed or already elapsed.
	function retryAfterSeconds(raw: string | null): number {
		if (!raw) return 0;
		let wait = parseInt(raw, 10);
		if (Number.isNaN(wait)) {
			const at = Date.parse(raw);
			wait = Number.isNaN(at) ? 0 : Math.ceil((at - Date.now()) / 1000);
		}
		return Math.min(Math.max(wait, 0), 3600);
	}

	type Provider = { id: string; name: string; type: string };

	let dark = $state(false);
	let screen = $state<'login' | 'forgot' | 'sent'>('login');

	let email = $state('');
	let password = $state('');
	let error = $state('');
	let notice = $state('');
	let shakeField = $state('');
	let busy = $state(false);
	let retryIn = $state(0);
	let providers = $state<Provider[]>([]);

	let retryTimer: ReturnType<typeof setInterval> | undefined;

	onMount(() => {
		dark = document.body.classList.contains('dark');

		// Shown once after /reset-password redirects here, then dropped from the address bar.
		const params = new URLSearchParams(window.location.search);
		if (params.get('reset') === '1') {
			notice = 'Password changed. Sign in with your new password.';
			history.replaceState(null, '', '/login');
		}

		loadProviders();

		// Validate a stored token before leaving. A stale token (another
		// deployment, a revoked session) would otherwise bounce
		// /login → /sql → /login forever. Keep the page usable while the check
		// runs, and clear the rejected session instead of redirecting blind.
		if (localStorage.getItem('token')) {
			client.GET('/auth/me').then(({ data }) => {
				if (!data) {
					clearSession();
					return;
				}
				const next = new URLSearchParams(window.location.search).get('next');
				window.location.href = next && next.startsWith('/') ? next : '/sql';
			});
		}

		return () => clearInterval(retryTimer);
	});

	async function loadProviders() {
		try {
			const { data } = await client.GET('/auth/oidc/providers');
			providers = (data as Provider[] | undefined) ?? [];
		} catch {
			// No providers means no SSO section; password login still works.
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
		error = `Too many attempts. Try again in ${retryIn} s.`;
		retryTimer = setInterval(() => {
			retryIn -= 1;
			if (retryIn <= 0) {
				clearInterval(retryTimer);
				retryIn = 0;
				error = '';
			} else {
				error = `Too many attempts. Try again in ${retryIn} s.`;
			}
		}, 1000);
	}

	async function doLogin() {
		if (busy || retryIn > 0) return;
		if (!email || !password) {
			showError('Please fill all fields.', 'password');
			return;
		}
		busy = true;
		error = '';
		notice = '';
		try {
			const { data, response } = await client.POST('/auth/login', {
				body: { email, password }
			});
			if (response.status === 401) {
				showError('Invalid e-mail or password.', 'password');
				return;
			}
			if (response.status === 429) {
				const wait = retryAfterSeconds(response.headers.get('Retry-After'));
				if (wait > 0) startRetryCountdown(wait);
				else showError('Too many attempts. Try again later.');
				return;
			}
			if (!response.ok || !data) {
				showError(`Sign in failed (HTTP ${response.status}). Try again.`);
				return;
			}
			setAccessToken(data.token);
			setRefreshToken(data.refresh_token);
			window.location.href = '/sql';
		} catch {
			showError('Connection error. Is the server running?');
		} finally {
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
				showError(
					'Password reset by e-mail is not configured on this server. Ask an administrator to reset your password.'
				);
			} else {
				showError(`Could not request a reset link (HTTP ${response.status}). Try again.`);
			}
		} catch {
			showError('Connection error. Is the server running?');
		} finally {
			busy = false;
		}
	}

	function doSSO(providerId: string) {
		window.location.href = `${API_ROOT}/auth/oidc/${encodeURIComponent(providerId)}/login`;
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

	const inputBase =
		'w-full h-10 bg-bg-app dark:bg-[#1c2128] border border-border rounded-[7px] px-3 text-[13px] text-text-main outline-none transition-all duration-150 placeholder:text-text-muted focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.1)] focus:bg-bg-panel';
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
			<div
				class="w-[72px] h-[72px] bg-primary/15 border border-primary/30 rounded-[20px] flex items-center justify-center"
			>
				<svg viewBox="0 0 24 24" class="w-9 h-9 stroke-primary"
					><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" /></svg
				>
			</div>
			<div class="text-[32px] font-bold tracking-[-1px] text-slate-100">Baleia</div>
			<div class="text-[13px] text-slate-500 leading-[1.6] max-w-[260px]">
				A powerful SQL client for your self-hosted data infrastructure.
			</div>
			<div
				class="font-mono text-[11px] text-slate-700 bg-white/[.04] border border-slate-800 py-1 px-3 rounded-[20px]"
			>
				v1.0.0-beta
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
			<div
				role="switch"
				aria-checked={dark}
				tabindex="0"
				onclick={toggleDark}
				onkeydown={(e) => e.key === 'Enter' && toggleDark()}
				class="w-8 h-[18px] rounded-full cursor-pointer relative transition-colors duration-200
					{dark ? 'bg-primary' : 'bg-border'}"
			>
				<span
					class="absolute w-[14px] h-[14px] bg-white rounded-full top-[2px] left-[2px] shadow-sm transition-transform duration-200
					{dark ? 'translate-x-[14px]' : 'translate-x-0'}"
				></span>
			</div>
		</div>

		<div class="w-full max-w-[360px]">
			{#if screen === 'login'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Sign in</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						Welcome back. Enter your credentials to continue.
					</div>
					{#if notice}
						<div
							class="bg-green-50 border border-green-200 rounded-md py-[9px] px-3 text-xs text-green-700 mb-3.5"
						>
							{notice}
						</div>
					{/if}
					{#if error}
						<div
							class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5"
						>
							{error}
						</div>
					{/if}
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Email</label>
						<input
							class="{inputBase} font-mono text-xs"
							type="email"
							bind:value={email}
							placeholder="you@company.com"
							onkeydown={(e) => e.key === 'Enter' && doLogin()}
						/>
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Password</label>
						<div class="relative">
							<input
								class="{inputBase} {shakeField === 'password' ? 'animate-[shake_.35s_ease]' : ''}"
								type="password"
								bind:value={password}
								placeholder="••••••••"
								onkeydown={(e) => e.key === 'Enter' && doLogin()}
							/>
						</div>
					</div>
					<div class="text-right text-[11px] -mt-2 mb-4">
						<span
							class="text-primary cursor-pointer font-semibold hover:text-primary-hover"
							onclick={() => go('forgot')}>Forgot password?</span
						>
					</div>
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
						{busy ? 'Signing in…' : 'Sign in'}
					</button>
					{#if providers.length > 0}
						<div
							class="flex items-center gap-2.5 my-4 text-[11px] font-semibold text-text-muted
							before:content-[''] before:flex-1 before:h-px before:bg-border
							after:content-[''] after:flex-1 after:h-px after:bg-border"
						>
							or
						</div>
						<div class="flex flex-col gap-2">
							{#each providers as p (p.id)}
								<button
									onclick={() => doSSO(p.id)}
									class="w-full h-10 bg-transparent border border-border rounded-[7px] text-[13px] font-medium text-text-main cursor-pointer transition-all duration-150 flex items-center justify-center gap-[9px] hover:border-border-hover hover:bg-bg-app"
								>
									Continue with {p.name}
								</button>
							{/each}
						</div>
					{/if}
				</div>
			{/if}

			{#if screen === 'forgot'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div
						class="flex items-center gap-[5px] text-xs font-semibold text-text-muted cursor-pointer mb-5 w-fit transition-colors duration-150 hover:text-text-main"
						onclick={() => go('login')}
					>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"
							><polyline points="15 18 9 12 15 6" /></svg
						>
						Back to sign in
					</div>
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Forgot password?</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						Enter your email and we'll send a link to reset your password.
					</div>
					{#if error}
						<div
							class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5"
						>
							{error}
						</div>
					{/if}
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Email</label>
						<input
							class="{inputBase} font-mono text-xs"
							type="email"
							bind:value={email}
							placeholder="you@company.com"
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
						{busy ? 'Sending…' : 'Send reset link'}
					</button>
				</div>
			{/if}

			{#if screen === 'sent'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Check your email</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						If an account exists for this address, we sent a link to reset the password. The link
						expires in 1 hour.
					</div>
					<button
						onclick={() => go('login')}
						class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover"
					>
						Back to sign in
					</button>
				</div>
			{/if}
		</div>
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
