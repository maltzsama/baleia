<script lang="ts">
	let dark = $state(false);
	let screen = $state<'login' | 'sso' | 'forgot' | 'code' | 'newpw' | 'success'>('login');
	let ssoProto = $state<'saml' | 'oidc'>('saml');

	let email = $state('');
	let password = $state('');
	let domain = $state('');
	let codeDigits = $state(['', '', '', '', '', '']);
	let newPassword = $state('');
	let confirmPassword = $state('');
	let error = $state('');
	let shakeField = $state('');
	let strengthScore = $state(0);

	const apiBase = 'http://localhost:8080/api/v1';

	function showError(msg: string, field = '') {
		error = msg;
		if (field) {
			shakeField = field;
			setTimeout(() => shakeField = '', 400);
		}
	}

	async function doLogin() {
		if (!email || !password) {
			showError('Please fill all fields.', 'password');
			return;
		}
		try {
			const res = await fetch(`${apiBase}/auth/login`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ email, password })
			});
			if (!res.ok) {
				showError('Invalid email or password.', 'password');
				return;
			}
			const data = await res.json();
			localStorage.setItem('token', data.token);
			localStorage.setItem('refresh_token', data.refresh_token);
			window.location.href = '/';
		} catch {
			showError('Connection error. Is the server running?', 'password');
		}
	}

	async function sendCode() {
		if (!email) return;
		try {
			await fetch(`${apiBase}/auth/password-reset/request`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ email })
			});
		} catch { /* 204 */ }
		screen = 'code';
	}

	function verifyCode() {
		const code = codeDigits.join('');
		if (code !== '123456') {
			showError('Invalid code.', 'code');
			return;
		}
		screen = 'newpw';
	}

	async function setNewPassword() {
		if (!newPassword || newPassword !== confirmPassword) {
			showError("Passwords don't match.", 'confirm');
			return;
		}
		screen = 'success';
	}

	function checkStrength(val: string) {
		let s = 0;
		if (val.length >= 8) s++;
		if (/[A-Z]/.test(val) && /[0-9]/.test(val)) s++;
		if (/[^A-Za-z0-9]/.test(val) && val.length >= 12) s++;
		strengthScore = s;
	}

	function doSSO() {
		if (!domain) return;
		window.location.href = `${apiBase}/auth/oidc/${domain}/login`;
	}

	function go(s: typeof screen) {
		error = '';
		screen = s;
	}

	function toggleDark() {
		dark = !dark;
		document.documentElement.classList.toggle('dark', dark);
	}

	function updateCodeDigit(i: number, val: string) {
		codeDigits[i] = val.replace(/\D/g, '').slice(-1);
		codeDigits = [...codeDigits];
		if (val && i < 5) {
			const next = document.querySelector<HTMLInputElement>(`[data-code="${i + 1}"]`);
			next?.focus();
		}
	}

	function handleCodePaste(e: ClipboardEvent) {
		e.preventDefault();
		const digits = e.clipboardData?.getData('text').replace(/\D/g, '').slice(0, 6) || '';
		digits.split('').forEach((ch, j) => { codeDigits[j] = ch; });
		codeDigits = [...codeDigits];
		const idx = Math.min(digits.length, 5);
		const el = document.querySelector<HTMLInputElement>(`[data-code="${idx}"]`);
		el?.focus();
	}

	function handleCodeKeydown(e: KeyboardEvent, i: number) {
		if (e.key === 'Backspace' && !codeDigits[i] && i > 0) {
			const prev = document.querySelector<HTMLInputElement>(`[data-code="${i - 1}"]`);
			prev?.focus();
		}
		if (e.key === 'Enter') verifyCode();
	}

	$effect(() => {
		if (screen === 'code') {
			setTimeout(() => {
				document.querySelector<HTMLInputElement>('[data-code="0"]')?.focus();
			}, 100);
		}
		if (screen === 'newpw') {
			setTimeout(() => {
				document.querySelector<HTMLInputElement>('#new-pw')?.focus();
			}, 100);
		}
	});

	const inputBase = "w-full h-10 bg-bg-app dark:bg-[#1c2128] border border-border rounded-[7px] px-3 text-[13px] text-text-main outline-none transition-all duration-150 placeholder:text-text-muted focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.1)] focus:bg-bg-panel";
</script>

<div class="bg-bg-app text-text-main min-h-screen flex overflow-hidden transition-colors duration-200" class:dark>

	<!-- Left branding -->
	<div class="flex-1 bg-brand-dark flex flex-col items-center justify-center p-12 relative overflow-hidden
		before:content-[''] before:absolute before:inset-0
		before:[background-image:linear-gradient(rgba(59,130,246,.06)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,.06)_1px,transparent_1px)]
		before:bg-[length:40px_40px]
		after:content-[''] after:absolute after:w-80 after:h-80 after:rounded-full
		after:[background:radial-gradient(circle,rgba(59,130,246,.18)_0%,transparent_70%)]
		after:top-1/2 after:left-1/2 after:-translate-x-1/2 after:-translate-y-1/2 after:pointer-events-none
		max-[680px]:hidden">
		<div class="relative z-[1] flex flex-col items-center text-center gap-6">
			<div class="w-[72px] h-[72px] bg-primary/15 border border-primary/30 rounded-[20px] flex items-center justify-center">
				<svg viewBox="0 0 24 24" class="w-9 h-9 stroke-primary"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>
			</div>
			<div class="text-[32px] font-bold tracking-[-1px] text-slate-100">Baleia</div>
			<div class="text-[13px] text-slate-500 leading-[1.6] max-w-[260px]">A powerful SQL client for your self-hosted data infrastructure.</div>
			<div class="font-mono text-[11px] text-slate-700 bg-white/[.04] border border-slate-800 py-1 px-3 rounded-[20px]">v1.0.0-beta</div>
		</div>
	</div>

	<!-- Right form -->
	<div class="w-[420px] shrink-0 flex flex-col items-center justify-center py-10 px-8 overflow-y-auto relative">

		<div class="absolute top-4 right-5 flex items-center gap-1.5 text-[11px] text-text-muted">
			<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
			<div
				role="switch"
				tabindex="0"
				onclick={toggleDark}
				onkeydown={(e) => e.key === 'Enter' && toggleDark()}
				class="w-8 h-[18px] rounded-full cursor-pointer relative transition-colors duration-200
					{dark ? 'bg-primary' : 'bg-border'}">
				<span class="absolute w-[14px] h-[14px] bg-white rounded-full top-[2px] left-[2px] shadow-sm transition-transform duration-200
					{dark ? 'translate-x-[14px]' : 'translate-x-0'}"></span>
			</div>
		</div>

		<div class="w-full max-w-[360px]">

			{#if screen === 'login'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Sign in</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">Welcome back. Enter your credentials to continue.</div>
					{#if error && shakeField === 'password'}
						<div class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5">{error}</div>
					{/if}
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Email</label>
						<input class="{inputBase} font-mono text-xs" type="email" bind:value={email} placeholder="you@company.com" onkeydown={(e) => e.key === 'Enter' && doLogin()}>
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Password</label>
						<div class="relative">
							<input
								class="{inputBase} {shakeField === 'password' ? 'animate-[shake_.35s_ease]' : ''}"
								type="password" bind:value={password} placeholder="••••••••"
								onkeydown={(e) => e.key === 'Enter' && doLogin()}>
						</div>
					</div>
					<div class="text-right text-[11px] -mt-2 mb-4">
						<span class="text-primary cursor-pointer font-semibold hover:text-primary-hover" onclick={() => go('forgot')}>Forgot password?</span>
					</div>
					<button onclick={doLogin} class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover">
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/><polyline points="10 17 15 12 10 7"/><line x1="15" y1="12" x2="3" y2="12"/></svg>
						Sign in
					</button>
					<div class="flex items-center gap-2.5 my-4 text-[11px] font-semibold text-text-muted
						before:content-[''] before:flex-1 before:h-px before:bg-border
						after:content-[''] after:flex-1 after:h-px after:bg-border">
						or
					</div>
					<div class="flex flex-col gap-2">
						<button onclick={() => window.location.href = `${apiBase}/auth/oidc/google/login`}
							class="w-full h-10 bg-transparent border border-border rounded-[7px] text-[13px] font-medium text-text-main cursor-pointer transition-all duration-150 flex items-center justify-center gap-[9px] hover:border-border-hover hover:bg-bg-app">
							Sign in with Google
						</button>
						<button onclick={() => window.location.href = `${apiBase}/auth/oidc/github/login`}
							class="w-full h-10 bg-transparent border border-border rounded-[7px] text-[13px] font-medium text-text-main cursor-pointer transition-all duration-150 flex items-center justify-center gap-[9px] hover:border-border-hover hover:bg-bg-app">
							Sign in with GitHub
						</button>
						<div class="mt-2">
							<div class="text-[10px] font-bold uppercase tracking-[.6px] text-text-muted mb-2 flex items-center gap-2
								after:content-[''] after:flex-1 after:h-px after:bg-border">
								Enterprise SSO
							</div>
							<button onclick={() => go('sso')}
								class="w-full h-10 bg-transparent border border-border rounded-[7px] text-xs font-semibold text-text-main cursor-pointer transition-all duration-150 flex items-center justify-center gap-2
									hover:border-primary hover:text-primary hover:bg-[#f8fbff] dark:hover:bg-[#152236]">
								Sign in with SSO
								<span class="ml-auto flex gap-1">
									<span class="text-[9px] font-bold font-mono py-0.5 px-1.5 rounded bg-blue-50 text-blue-700">SAML</span>
									<span class="text-[9px] font-bold font-mono py-0.5 px-1.5 rounded bg-fuchsia-50 text-purple-700">OIDC</span>
								</span>
							</button>
						</div>
					</div>
				</div>
			{/if}

			{#if screen === 'sso'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="flex items-center gap-[5px] text-xs font-semibold text-text-muted cursor-pointer mb-5 w-fit transition-colors duration-150 hover:text-text-main" onclick={() => go('login')}>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="15 18 9 12 15 6"/></svg>
						Back
					</div>
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Enterprise SSO</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">Enter your organization's domain and select the protocol configured by your admin.</div>
					<div class="flex border border-border rounded-[7px] overflow-hidden mb-4">
						<button onclick={() => ssoProto = 'saml'}
							class="flex-1 h-9 border-none text-xs font-semibold cursor-pointer transition-all duration-150 border-r border-border last:border-r-0
								{ssoProto === 'saml' ? 'bg-primary text-white' : 'bg-transparent text-text-muted hover:bg-bg-app'}">
							SAML 2.0
						</button>
						<button onclick={() => ssoProto = 'oidc'}
							class="flex-1 h-9 border-none text-xs font-semibold cursor-pointer transition-all duration-150
								{ssoProto === 'oidc' ? 'bg-primary text-white' : 'bg-transparent text-text-muted hover:bg-bg-app'}">
							OIDC
						</button>
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Organization domain</label>
						<input class="{inputBase} font-mono text-xs" type="text" bind:value={domain} placeholder="company.com" onkeydown={(e) => e.key === 'Enter' && doSSO()}>
						<div class="text-[11px] text-text-muted mt-1">
							{ssoProto === 'saml' ? "We'll redirect you to your SAML identity provider." : "We'll redirect you to your OIDC authorization endpoint."}
						</div>
					</div>
					<button onclick={doSSO} class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover">
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/><polyline points="10 17 15 12 10 7"/><line x1="15" y1="12" x2="3" y2="12"/></svg>
						Continue with SSO
					</button>
				</div>
			{/if}

			{#if screen === 'forgot'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="flex items-center gap-[5px] text-xs font-semibold text-text-muted cursor-pointer mb-5 w-fit transition-colors duration-150 hover:text-text-main" onclick={() => go('login')}>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="15 18 9 12 15 6"/></svg>
						Back to sign in
					</div>
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Forgot password?</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">Enter your email and we'll send a 6-digit code to reset your password.</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Email</label>
						<input class="{inputBase} font-mono text-xs" type="email" bind:value={email} placeholder="you@company.com" onkeydown={(e) => e.key === 'Enter' && sendCode()}>
					</div>
					<button onclick={sendCode} class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover">
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
						Send code
					</button>
				</div>
			{/if}

			{#if screen === 'code'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="flex items-center gap-[5px] text-xs font-semibold text-text-muted cursor-pointer mb-5 w-fit transition-colors duration-150 hover:text-text-main" onclick={() => go('forgot')}>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="15 18 9 12 15 6"/></svg>
						Back
					</div>
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Check your email</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">We sent a 6-digit code to <strong>{email}</strong>. It expires in 10 minutes.</div>
					{#if error && shakeField === 'code'}
						<div class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5">{error}</div>
					{/if}
					<div class="flex gap-2 justify-center mb-2 {shakeField === 'code' ? 'animate-[shake_.35s_ease]' : ''}">
						{#each codeDigits as _, i}
							<input
								class="w-[46px] h-[54px] bg-bg-app dark:bg-[#1c2128] border border-border rounded-lg font-mono text-2xl font-bold text-center text-text-main outline-none transition-all duration-150
									focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.1)] focus:bg-bg-panel"
								data-code={i}
								maxlength="1"
								type="text"
								inputmode="numeric"
								value={codeDigits[i]}
								oninput={(e) => updateCodeDigit(i, (e.target as HTMLInputElement).value)}
								onkeydown={(e) => handleCodeKeydown(e, i)}
								onpaste={i === 0 ? handleCodePaste : undefined}
							>
						{/each}
					</div>
					<div class="text-xs text-text-muted text-center mb-5">Didn't receive it? <span class="text-primary cursor-pointer font-semibold hover:text-primary-hover" onclick={sendCode}>Resend code</span></div>
					<button disabled={codeDigits.some(d => !d)} onclick={verifyCode}
						class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5
							hover:enabled:bg-primary-hover disabled:bg-blue-300 disabled:cursor-not-allowed">
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><polyline points="20 6 9 17 4 12"/></svg>
						Verify
					</button>
				</div>
			{/if}

			{#if screen === 'newpw'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="flex items-center gap-[5px] text-xs font-semibold text-text-muted cursor-pointer mb-5 w-fit transition-colors duration-150 hover:text-text-main" onclick={() => go('login')}>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="15 18 9 12 15 6"/></svg>
						Back to sign in
					</div>
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Set new password</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">Choose a strong password for your account.</div>
					{#if error && shakeField === 'confirm'}
						<div class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5">{error}</div>
					{/if}
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">New password</label>
						<div class="relative">
							<input id="new-pw" class={inputBase} type="password" bind:value={newPassword} placeholder="••••••••"
								oninput={(e) => checkStrength((e.target as HTMLInputElement).value)}
								onkeydown={(e) => e.key === 'Enter' && setNewPassword()}>
						</div>
						<div class="flex gap-1 mt-1.5">
							<div class="flex-1 h-[3px] rounded-sm transition-colors duration-300
								{strengthScore >= 3 ? 'bg-green-500' : (strengthScore >= 1 ? 'bg-red-500' : 'bg-border')}"></div>
							<div class="flex-1 h-[3px] rounded-sm transition-colors duration-300
								{strengthScore >= 3 ? 'bg-green-500' : (strengthScore >= 2 ? 'bg-amber-500' : 'bg-border')}"></div>
							<div class="flex-1 h-[3px] rounded-sm transition-colors duration-300
								{strengthScore >= 3 ? 'bg-green-500' : 'bg-border'}"></div>
						</div>
						<div class="text-[10px] mt-1">
							{#if strengthScore === 1}<span class="text-red-500">Weak</span>
							{:else if strengthScore === 2}<span class="text-amber-500">Fair</span>
							{:else if strengthScore >= 3}<span class="text-green-600">Strong</span>
							{/if}
						</div>
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Confirm password</label>
						<div class="relative">
							<input class={inputBase} type="password" bind:value={confirmPassword} placeholder="••••••••" onkeydown={(e) => e.key === 'Enter' && setNewPassword()}>
						</div>
					</div>
					<button onclick={setNewPassword} class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover">
						<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
						Update password
					</button>
				</div>
			{/if}

			{#if screen === 'success'}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="w-[52px] h-[52px] rounded-full bg-green-50 border border-green-200 flex items-center justify-center mx-auto mb-4">
						<svg viewBox="0 0 24 24" class="w-[22px] h-[22px] stroke-green-600"><polyline points="20 6 9 17 4 12"/></svg>
					</div>
					<div class="text-xl font-bold tracking-[-.4px] mb-1 text-center">Password updated</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55] text-center">Your password has been reset. Sign in with your new password.</div>
					<button onclick={() => go('login')} class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5 hover:bg-primary-hover">
						Back to sign in
					</button>
				</div>
			{/if}

		</div>
	</div>
</div>

<style>
	@keyframes fadeIn { from { opacity: 0; transform: translateY(6px) } to { opacity: 1; transform: translateY(0) } }
	@keyframes shake { 0%,100%{transform:translateX(0)} 20%,60%{transform:translateX(-5px)} 40%,80%{transform:translateX(5px)} }
</style>