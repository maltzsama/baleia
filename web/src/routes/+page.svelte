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
			window.location.href = '/app';
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
</script>

<div class="app" class:dark>
	<!-- Left branding -->
	<div class="left">
		<div class="left-inner">
			<div class="logo-mark">
				<svg viewBox="0 0 24 24"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>
			</div>
			<div class="brand-name-lg">Baleia</div>
			<div class="brand-tag">A powerful SQL client for your self-hosted data infrastructure.</div>
			<div class="brand-version">v1.0.0-beta</div>
		</div>
	</div>

	<!-- Right form -->
	<div class="right">
		<div class="dark-toggle-wrap">
			<svg viewBox="0 0 24 24" class="dark-icon"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
			<div class="dtoggle" class:on={dark} onclick={toggleDark} role="switch" tabindex="0" onkeydown={(e) => e.key === 'Enter' && toggleDark()}></div>
		</div>

		<div class="form-wrap">
			{#if screen === 'login'}
				<div class="screen active">
					<div class="screen-title">Sign in</div>
					<div class="screen-sub">Welcome back. Enter your credentials to continue.</div>
					{#if error && shakeField === 'password'}
						<div class="error-msg show">{error}</div>
					{/if}
					<div class="field">
						<label class="label">Email</label>
						<input class="input mono" type="email" bind:value={email} placeholder="you@company.com" onkeydown={(e) => e.key === 'Enter' && doLogin()}>
					</div>
					<div class="field">
						<label class="label">Password</label>
						<div class="pw-wrap">
							<input class="input" class:shake={shakeField === 'password'} type="password" bind:value={password} placeholder="••••••••" onkeydown={(e) => e.key === 'Enter' && doLogin()}>
						</div>
					</div>
					<div class="forgot"><span class="link" onclick={() => go('forgot')}>Forgot password?</span></div>
					<button class="btn btn-primary" onclick={doLogin}>
						<svg viewBox="0 0 24 24"><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/><polyline points="10 17 15 12 10 7"/><line x1="15" y1="12" x2="3" y2="12"/></svg>
						Sign in
					</button>
					<div class="or-divider">or</div>
					<div class="sso-group">
						<button class="btn-sso" onclick={() => window.location.href = `${apiBase}/auth/oidc/google/login`}>
							Sign in with Google
						</button>
						<button class="btn-sso" onclick={() => window.location.href = `${apiBase}/auth/oidc/github/login`}>
							Sign in with GitHub
						</button>
						<div class="sso-enterprise">
							<div class="sso-label">Enterprise SSO</div>
							<button class="btn-enterprise" onclick={() => go('sso')}>
								Sign in with SSO
								<span class="proto-badges">
									<span class="proto-badge pb-saml">SAML</span>
									<span class="proto-badge pb-oidc">OIDC</span>
								</span>
							</button>
						</div>
					</div>
				</div>
			{/if}

			{#if screen === 'sso'}
				<div class="screen active">
					<div class="back-link" onclick={() => go('login')}>
						<svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
						Back
					</div>
					<div class="screen-title">Enterprise SSO</div>
					<div class="screen-sub">Enter your organization's domain and select the protocol configured by your admin.</div>
					<div class="sso-proto-tabs">
						<button class="proto-tab" class:on={ssoProto === 'saml'} onclick={() => ssoProto = 'saml'}>SAML 2.0</button>
						<button class="proto-tab" class:on={ssoProto === 'oidc'} onclick={() => ssoProto = 'oidc'}>OIDC</button>
					</div>
					<div class="field">
						<label class="label">Organization domain</label>
						<input class="input mono" type="text" bind:value={domain} placeholder="company.com" onkeydown={(e) => e.key === 'Enter' && doSSO()}>
						<div class="hint">{ssoProto === 'saml' ? "We'll redirect you to your SAML identity provider." : "We'll redirect you to your OIDC authorization endpoint."}</div>
					</div>
					<button class="btn btn-primary" onclick={doSSO}>
						<svg viewBox="0 0 24 24"><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/><polyline points="10 17 15 12 10 7"/><line x1="15" y1="12" x2="3" y2="12"/></svg>
						Continue with SSO
					</button>
				</div>
			{/if}

			{#if screen === 'forgot'}
				<div class="screen active">
					<div class="back-link" onclick={() => go('login')}>
						<svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
						Back to sign in
					</div>
					<div class="screen-title">Forgot password?</div>
					<div class="screen-sub">Enter your email and we'll send a 6-digit code to reset your password.</div>
					<div class="field">
						<label class="label">Email</label>
						<input class="input mono" type="email" bind:value={email} placeholder="you@company.com" onkeydown={(e) => e.key === 'Enter' && sendCode()}>
					</div>
					<button class="btn btn-primary" onclick={sendCode}>
						<svg viewBox="0 0 24 24"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
						Send code
					</button>
				</div>
			{/if}

			{#if screen === 'code'}
				<div class="screen active">
					<div class="back-link" onclick={() => go('forgot')}>
						<svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
						Back
					</div>
					<div class="screen-title">Check your email</div>
					<div class="screen-sub">We sent a 6-digit code to <strong>{email}</strong>. It expires in 10 minutes.</div>
					{#if error && shakeField === 'code'}
						<div class="error-msg show">{error}</div>
					{/if}
					<div class="code-inputs" class:shake={shakeField === 'code'}>
						{#each codeDigits as _, i}
							<input
								class="code-digit"
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
					<div class="resend">Didn't receive it? <span class="link" onclick={sendCode}>Resend code</span></div>
					<button class="btn btn-primary" disabled={codeDigits.some(d => !d)} onclick={verifyCode}>
						<svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>
						Verify
					</button>
				</div>
			{/if}

			{#if screen === 'newpw'}
				<div class="screen active">
					<div class="back-link" onclick={() => go('login')}>
						<svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
						Back to sign in
					</div>
					<div class="screen-title">Set new password</div>
					<div class="screen-sub">Choose a strong password for your account.</div>
					{#if error && shakeField === 'confirm'}
						<div class="error-msg show">{error}</div>
					{/if}
					<div class="field">
						<label class="label">New password</label>
						<div class="pw-wrap">
							<input id="new-pw" class="input" type="password" bind:value={newPassword} placeholder="••••••••" oninput={(e) => checkStrength((e.target as HTMLInputElement).value)} onkeydown={(e) => e.key === 'Enter' && setNewPassword()}>
						</div>
						<div class="pw-strength">
							<div class="pw-bar" class:weak={strengthScore >= 1 && strengthScore < 3} class:medium={strengthScore === 2} class:strong={strengthScore >= 3}></div>
							<div class="pw-bar" class:weak={strengthScore >= 2 && strengthScore < 3} class:medium={strengthScore >= 2} class:strong={strengthScore >= 3}></div>
							<div class="pw-bar" class:weak={strengthScore >= 3} class:strong={strengthScore >= 3}></div>
						</div>
						<div class="pw-strength-label">
							{#if strengthScore === 1}<span style="color:#ef4444">Weak</span>
							{:else if strengthScore === 2}<span style="color:#f59e0b">Fair</span>
							{:else if strengthScore >= 3}<span style="color:#16a34a">Strong</span>
							{/if}
						</div>
					</div>
					<div class="field">
						<label class="label">Confirm password</label>
						<div class="pw-wrap">
							<input class="input" type="password" bind:value={confirmPassword} placeholder="••••••••" onkeydown={(e) => e.key === 'Enter' && setNewPassword()}>
						</div>
					</div>
					<button class="btn btn-primary" onclick={setNewPassword}>
						<svg viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
						Update password
					</button>
				</div>
			{/if}

			{#if screen === 'success'}
				<div class="screen active">
					<div class="success-icon"><svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg></div>
					<div class="screen-title center">Password updated</div>
					<div class="screen-sub center">Your password has been reset. Sign in with your new password.</div>
					<button class="btn btn-primary" onclick={() => go('login')}>Back to sign in</button>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	:root {
		--bg-app: #f0f2f5; --bg-panel: #fff;
		--border-light: #e2e8f0; --border-h: #cbd5e1;
		--text-main: #1e293b; --text-muted: #64748b;
		--primary: #3b82f6; --primary-h: #2563eb;
		--brand-dark: #0f172a;
	}
	.dark {
		--bg-app: #0d1117; --bg-panel: #161b22;
		--border-light: #30363d; --border-h: #484f58;
		--text-main: #e6edf3; --text-muted: #7d8590;
	}
	* { box-sizing: border-box; margin: 0; padding: 0; font-family: 'IBM Plex Sans', sans-serif; }
	svg { width: 14px; height: 14px; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; fill: none; flex-shrink: 0; }

	.app { background: var(--bg-app); color: var(--text-main); min-height: 100vh; display: flex; overflow: hidden; transition: background .2s; }

	.left {
		flex: 1; background: var(--brand-dark); display: flex; flex-direction: column;
		align-items: center; justify-content: center; padding: 48px; position: relative; overflow: hidden;
	}
	.left::before {
		content: ''; position: absolute; inset: 0;
		background-image: linear-gradient(rgba(59,130,246,.06) 1px, transparent 1px),
			linear-gradient(90deg, rgba(59,130,246,.06) 1px, transparent 1px);
		background-size: 40px 40px;
	}
	.left::after {
		content: ''; position: absolute; width: 320px; height: 320px; border-radius: 50%;
		background: radial-gradient(circle, rgba(59,130,246,.18) 0%, transparent 70%);
		top: 50%; left: 50%; transform: translate(-50%, -50%); pointer-events: none;
	}
	.left-inner { position: relative; z-index: 1; display: flex; flex-direction: column; align-items: center; text-align: center; gap: 24px; }
	.logo-mark { width: 72px; height: 72px; background: rgba(59,130,246,.15); border: 1px solid rgba(59,130,246,.3); border-radius: 20px; display: flex; align-items: center; justify-content: center; }
	.logo-mark svg { width: 36px; height: 36px; stroke: #3b82f6; }
	.brand-name-lg { font-size: 32px; font-weight: 700; letter-spacing: -1px; color: #f1f5f9; }
	.brand-tag { font-size: 13px; color: #64748b; line-height: 1.6; max-width: 260px; }
	.brand-version { font-family: 'JetBrains Mono', monospace; font-size: 11px; color: #334155; background: rgba(255,255,255,.04); border: 1px solid #1e293b; padding: 4px 12px; border-radius: 20px; }

	.right { width: 420px; flex-shrink: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 40px 32px; overflow-y: auto; position: relative; }
	.dark-toggle-wrap { position: absolute; top: 16px; right: 20px; display: flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-muted); }
	.dark-icon { width: 13px; height: 13px; }
	.dtoggle { width: 32px; height: 18px; background: var(--border-light); border-radius: 9px; cursor: pointer; position: relative; transition: background .2s; }
	.dtoggle.on { background: var(--primary); }
	.dtoggle::after { content: ''; position: absolute; width: 14px; height: 14px; background: white; border-radius: 50%; top: 2px; left: 2px; transition: transform .2s; box-shadow: 0 1px 3px rgba(0,0,0,.2); }
	.dtoggle.on::after { transform: translateX(14px); }
	.form-wrap { width: 100%; max-width: 360px; }

	.screen { display: flex; flex-direction: column; animation: fadeIn .2s ease; }
	@keyframes fadeIn { from { opacity: 0; transform: translateY(6px) } to { opacity: 1; transform: translateY(0) } }
	.screen-title { font-size: 20px; font-weight: 700; letter-spacing: -.4px; margin-bottom: 4px; }
	.screen-sub { font-size: 13px; color: var(--text-muted); margin-bottom: 24px; line-height: 1.55; }
	.center { text-align: center; }

	.field { display: flex; flex-direction: column; gap: 5px; margin-bottom: 14px; }
	.label { font-size: 11px; font-weight: 600; }
	.input { width: 100%; height: 40px; background: var(--bg-app); border: 1px solid var(--border-light); border-radius: 7px; padding: 0 12px; font-size: 13px; color: var(--text-main); outline: none; transition: .15s; }
	.dark .input { background: #1c2128; }
	.input:focus { border-color: var(--primary); box-shadow: 0 0 0 3px rgba(59,130,246,.1); background: var(--bg-panel); }
	.dark .input:focus { background: var(--bg-panel); }
	.input::placeholder { color: var(--text-muted); }
	.input.mono { font-family: 'JetBrains Mono', monospace; font-size: 12px; }
	.pw-wrap { position: relative; }
	.forgot { text-align: right; font-size: 11px; margin-top: -8px; margin-bottom: 16px; }
	.link { color: var(--primary); cursor: pointer; font-weight: 600; }
	.link:hover { color: var(--primary-h); }

	.btn { width: 100%; height: 40px; border: none; border-radius: 7px; font-size: 13px; font-weight: 600; cursor: pointer; transition: .15s; display: flex; align-items: center; justify-content: center; gap: 7px; }
	.btn-primary { background: var(--primary); color: white; margin-bottom: 10px; }
	.btn-primary:hover { background: var(--primary-h); }
	.btn-primary:disabled { background: #93c5fd; cursor: not-allowed; }

	.or-divider { display: flex; align-items: center; gap: 10px; margin: 16px 0; font-size: 11px; font-weight: 600; color: var(--text-muted); }
	.or-divider::before, .or-divider::after { content: ''; flex: 1; height: 1px; background: var(--border-light); }

	.sso-group { display: flex; flex-direction: column; gap: 8px; }
	.btn-sso { width: 100%; height: 40px; background: transparent; border: 1px solid var(--border-light); border-radius: 7px; font-size: 13px; font-weight: 500; color: var(--text-main); cursor: pointer; transition: .15s; display: flex; align-items: center; justify-content: center; gap: 9px; }
	.btn-sso:hover { border-color: var(--border-h); background: var(--bg-app); }
	.sso-enterprise { margin-top: 8px; }
	.sso-label { font-size: 10px; font-weight: 700; text-transform: uppercase; letter-spacing: .6px; color: var(--text-muted); margin-bottom: 8px; display: flex; align-items: center; gap: 8px; }
	.sso-label::after { content: ''; flex: 1; height: 1px; background: var(--border-light); }
	.btn-enterprise { width: 100%; height: 40px; background: transparent; border: 1px solid var(--border-light); border-radius: 7px; font-size: 12px; font-weight: 600; color: var(--text-main); cursor: pointer; transition: .15s; display: flex; align-items: center; justify-content: center; gap: 8px; }
	.btn-enterprise:hover { border-color: var(--primary); color: var(--primary); background: #f8fbff; }
	.dark .btn-enterprise:hover { background: #152236; }
	.proto-badges { margin-left: auto; display: flex; gap: 4px; }
	.proto-badge { font-size: 9px; font-weight: 700; font-family: 'JetBrains Mono', monospace; padding: 2px 6px; border-radius: 4px; }
	.pb-saml { background: #eff6ff; color: #1d4ed8; }
	.pb-oidc { background: #fdf4ff; color: #7e22ce; }

	.sso-proto-tabs { display: flex; border: 1px solid var(--border-light); border-radius: 7px; overflow: hidden; margin-bottom: 16px; }
	.proto-tab { flex: 1; height: 36px; border: none; background: transparent; font-size: 12px; font-weight: 600; color: var(--text-muted); cursor: pointer; transition: .15s; border-right: 1px solid var(--border-light); }
	.proto-tab:last-child { border-right: none; }
	.proto-tab.on { background: var(--primary); color: white; }
	.proto-tab:not(.on):hover { background: var(--bg-app); }
	.hint { font-size: 11px; color: var(--text-muted); margin-top: 4px; }

	.back-link { display: flex; align-items: center; gap: 5px; font-size: 12px; font-weight: 600; color: var(--text-muted); cursor: pointer; margin-bottom: 20px; width: fit-content; transition: color .15s; }
	.back-link:hover { color: var(--text-main); }
	.back-link svg { width: 13px; height: 13px; }

	.code-inputs { display: flex; gap: 8px; justify-content: center; margin-bottom: 8px; }
	.code-digit { width: 46px; height: 54px; background: var(--bg-app); border: 1px solid var(--border-light); border-radius: 8px; font-family: 'JetBrains Mono', monospace; font-size: 22px; font-weight: 700; text-align: center; color: var(--text-main); outline: none; transition: .15s; }
	.dark .code-digit { background: #1c2128; }
	.code-digit:focus { border-color: var(--primary); box-shadow: 0 0 0 3px rgba(59,130,246,.1); background: var(--bg-panel); }
	.dark .code-digit:focus { background: var(--bg-panel); }
	.resend { font-size: 12px; color: var(--text-muted); text-align: center; margin-bottom: 20px; }

	.pw-strength { display: flex; gap: 4px; margin-top: 6px; }
	.pw-bar { flex: 1; height: 3px; border-radius: 2px; background: var(--border-light); transition: background .3s; }
	.pw-bar.weak { background: #ef4444; }
	.pw-bar.medium { background: #f59e0b; }
	.pw-bar.strong { background: #22c55e; }
	.pw-strength-label { font-size: 10px; margin-top: 4px; }

	.error-msg { background: #fef2f2; border: 1px solid #fecaca; border-radius: 6px; padding: 9px 12px; font-size: 12px; color: #dc2626; margin-bottom: 14px; display: none; }
	.error-msg.show { display: block; }

	.success-icon { width: 52px; height: 52px; border-radius: 50%; background: #f0fdf4; border: 1px solid #bbf7d0; display: flex; align-items: center; justify-content: center; margin: 0 auto 16px; }
	.success-icon svg { width: 22px; height: 22px; stroke: #16a34a; }

	@keyframes shake { 0%,100%{transform:translateX(0)} 20%,60%{transform:translateX(-5px)} 40%,80%{transform:translateX(5px)} }
	.shake { animation: shake .35s ease; }

	@media (max-width: 680px) {
		.left { display: none; }
	}
</style>
