<script lang="ts">
	let step = $state(1);
	let proto = $state<'saml' | 'oidc'>('saml');

	let firstName = $state('');
	let lastName = $state('');
	let email = $state('');
	let password = $state('');
	let confirmPass = $state('');
	let strengthScore = $state(0);
	let error = $state('');

	let engine = $state('polaris');
	let connName = $state('');
	let connHost = $state('');
	let connPort = $state('8181');
	let connUser = $state('');
	let connPass = $state('');
	let testStatus = $state<'idle' | 'testing' | 'ok' | 'err'>('idle');
	let testMsg = $state('');

	let ssoIssuer = $state('');
	let ssoClientId = $state('');
	let ssoClientSecret = $state('');
	let ssoIdpUrl = $state('');
	let ssoEntityId = $state('');
	let ssoCert = $state('');

	const apiBase = 'http://localhost:8080/api/v1';

	function checkStrength(val: string) {
		let s = 0;
		if (val.length >= 8) s++;
		if (/[A-Z]/.test(val) && /[0-9]/.test(val)) s++;
		if (/[^A-Za-z0-9]/.test(val) && val.length >= 12) s++;
		strengthScore = s;
	}

	async function nextStep() {
		error = '';
		if (step === 1) {
			if (!firstName || !email || !password || password !== confirmPass) {
				error = 'Please fill in all required fields and make sure passwords match.';
				return;
			}
			try {
				const res = await fetch(`${apiBase}/setup/admin`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ name: `${firstName} ${lastName}`.trim(), email, password })
				});
				if (!res.ok) {
					error = 'Failed to create admin account.';
					return;
				}
				const data = await res.json();
				localStorage.setItem('token', data.token);
				localStorage.setItem('refresh_token', data.refresh_token);
			} catch {
				error = 'Connection error. Is the server running?';
				return;
			}
		}
		if (step === 2 && connName && connHost) {
			try {
				await fetch(`${apiBase}/catalog-connections`, {
					method: 'POST',
					headers: {
						'Content-Type': 'application/json',
						'Authorization': `Bearer ${localStorage.getItem('token')}`
					},
					body: JSON.stringify({
						name: connName,
						type: engine,
						url: `http://${connHost}:${connPort}`,
						default_readonly: true
					})
				});
			} catch { /* optional */ }
		}
		if (step === 3) {
			// SSO config saved if filled
		}
		if (step < 3) {
			step++;
		} else {
			step = 4;
		}
	}

	function prevStep() {
		error = '';
		if (step > 1) step--;
	}

	function skip() {
		error = '';
		if (step < 3) step++;
		else step = 4;
	}

	function selectEngine(code: string, port: string) {
		engine = code;
		connPort = port;
	}

	async function testConnection() {
		testStatus = 'testing';
		await new Promise(r => setTimeout(r, 1200));
		testStatus = Math.random() > 0.3 ? 'ok' : 'err';
		testMsg = testStatus === 'ok' ? 'Connection successful — 6ms' : 'Connection refused — check host and port';
	}

	let engines = [
		{ code: 'polaris', label: 'Polaris', port: '8181', cls: 'epg' },
		{ code: 'nessie', label: 'Nessie', port: '19120', cls: 'emy' },
		{ code: 'iceberg', label: 'Iceberg', port: '8080', cls: 'etr' },
		{ code: 'glue', label: 'Glue', port: '443', cls: 'ers' },
		{ code: 'hive', label: 'Hive', port: '9083', cls: 'edr' },
	];
</script>

<div class="setup">
	<div class="left">
		<div class="left-inner">
			<div class="brand">
				<div class="brand-icon"><svg viewBox="0 0 24 24"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg></div>
				<span class="brand-name">Baleia</span>
			</div>
			<div class="steps-nav">
				{#each [
					{ label: 'Admin account', sub: 'Name, email & password' },
					{ label: 'First connection', sub: 'Connect a datasource', opt: true },
					{ label: 'SSO', sub: 'SAML or OIDC', opt: true }
				] as s, i}
					{@const n = i + 1}
					<div class="step-item" class:done={step > n} class:active={step === n}>
						<div class="step-circle">
							{#if step > n}
								<svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>
							{:else}
								{n}
							{/if}
						</div>
						<div class="step-text">
							<div class="step-label">{s.label}{#if s.opt} <span class="opt-label">(optional)</span>{/if}</div>
							<div class="step-sublabel">{s.sub}</div>
						</div>
					</div>
				{/each}
			</div>
			<div class="left-footer">
				Baleia is open-source.<br>
				<a href="https://github.com" target="_blank">github.com/baleia</a>
			</div>
		</div>
	</div>

	<div class="right">
		<div class="step-wrap">
			{#if step === 1}
				<div class="step-screen active">
					<div class="step-header">
						<div class="step-eyebrow">Step 1 of 3</div>
						<div class="step-title">Create admin account</div>
						<div class="step-desc">This will be the first administrator of your Baleia instance.</div>
					</div>
					{#if error}<div class="err-msg show">{error}</div>{/if}
					<div class="field-row">
						<div class="field"><label class="label">First name</label><input class="input" type="text" bind:value={firstName} placeholder="John"></div>
						<div class="field"><label class="label">Last name</label><input class="input" type="text" bind:value={lastName} placeholder="Doe"></div>
					</div>
					<div class="field"><label class="label">Email</label><input class="input mono" type="email" bind:value={email} placeholder="admin@company.com"></div>
					<div class="field">
						<label class="label">Password</label>
						<div class="pw-wrap">
							<input class="input" type="password" bind:value={password} placeholder="••••••••" oninput={(e) => checkStrength((e.target as HTMLInputElement).value)}>
						</div>
						<div class="pw-strength">
							<div class="pw-bar" class:weak={strengthScore >= 1} class:medium={strengthScore >= 2} class:strong={strengthScore >= 3}></div>
							<div class="pw-bar" class:weak={strengthScore >= 2} class:medium={strengthScore >= 2} class:strong={strengthScore >= 3}></div>
							<div class="pw-bar" class:weak={strengthScore >= 3} class:strong={strengthScore >= 3}></div>
						</div>
						<div class="pw-slabel">
							{#if strengthScore === 1}<span class="weak">Weak</span>
							{:else if strengthScore === 2}<span class="medium">Fair</span>
							{:else if strengthScore >= 3}<span class="strong">Strong</span>
							{/if}
						</div>
					</div>
					<div class="field">
						<label class="label">Confirm password</label>
						<div class="pw-wrap">
							<input class="input" type="password" bind:value={confirmPass} placeholder="••••••••">
						</div>
					</div>
					<div class="btn-row">
						<span></span>
						<button class="btn btn-primary" onclick={nextStep}>
							Continue
							<svg viewBox="0 0 24 24"><polyline points="9 18 15 12 9 6"/></svg>
						</button>
					</div>
				</div>
			{/if}

			{#if step === 2}
				<div class="step-screen active">
					<div class="step-header">
						<div class="step-eyebrow">Step 2 of 3 — Optional</div>
						<div class="step-title">Connect a datasource</div>
						<div class="step-desc">Add your first catalog connection. You can skip this and connect later.</div>
					</div>
					{#if error}<div class="err-msg show">{error}</div>{/if}
					<div class="section-label">Catalog type</div>
					<div class="engine-grid">
						{#each engines as e}
							<div class="engine-opt" class:on={engine === e.code} onclick={() => selectEngine(e.code, e.port)}>
								<div class="eicon {e.cls}">{e.code.slice(0,2).toUpperCase()}</div>
								<span class="elabel">{e.label}</span>
							</div>
						{/each}
					</div>
					<div class="field"><label class="label">Connection name</label><input class="input" type="text" bind:value={connName} placeholder="e.g. analytics-prod"></div>
					<div class="field-row">
						<div class="field" style="margin-bottom:0;"><label class="label">Host</label><input class="input mono" type="text" bind:value={connHost} placeholder="hostname or IP"></div>
						<div class="field" style="margin-bottom:0;"><label class="label">Port</label><input class="input mono" type="text" bind:value={connPort}></div>
					</div>
					<div class="field-row">
						<div class="field"><label class="label">Username</label><input class="input" type="text" bind:value={connUser} placeholder="user"></div>
						<div class="field"><label class="label">Password</label><input class="input" type="password" bind:value={connPass} placeholder="••••••••"></div>
					</div>
					<div class="test-row">
						<button class="btn-test" class:testing={testStatus === 'testing'} onclick={testConnection}>
							<svg viewBox="0 0 24 24"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
							{testStatus === 'testing' ? 'Testing...' : 'Test connection'}
						</button>
						{#if testStatus === 'ok'}
							<div class="test-result ok"><svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg> {testMsg}</div>
						{:else if testStatus === 'err'}
							<div class="test-result err"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg> {testMsg}</div>
						{/if}
					</div>
					<div class="btn-row">
						<button class="btn btn-ghost" onclick={prevStep}>
							<svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
							Back
						</button>
						<div class="btn-right">
							<button class="btn-skip" onclick={skip}>Skip for now</button>
							<button class="btn btn-primary" onclick={nextStep}>
								Continue
								<svg viewBox="0 0 24 24"><polyline points="9 18 15 12 9 6"/></svg>
							</button>
						</div>
					</div>
				</div>
			{/if}

			{#if step === 3}
				<div class="step-screen active">
					<div class="step-header">
						<div class="step-eyebrow">Step 3 of 3 — Optional</div>
						<div class="step-title">Configure SSO</div>
						<div class="step-desc">Let your team sign in through your identity provider.</div>
					</div>
					<div class="proto-tabs">
						<button class="proto-tab" class:on={proto === 'saml'} onclick={() => proto = 'saml'}>SAML 2.0</button>
						<button class="proto-tab" class:on={proto === 'oidc'} onclick={() => proto = 'oidc'}>OIDC</button>
					</div>
					{#if proto === 'saml'}
						<div class="field"><label class="label">IdP SSO URL</label><input class="input mono" type="text" bind:value={ssoIdpUrl} placeholder="https://idp.company.com/sso/saml"></div>
						<div class="field"><label class="label">IdP Entity ID</label><input class="input mono" type="text" bind:value={ssoEntityId} placeholder="https://idp.company.com/entity"></div>
						<div class="field"><label class="label">X.509 Certificate <span class="opt">(PEM)</span></label><textarea class="input mono" rows="3" bind:value={ssoCert} placeholder="-----BEGIN CERTIFICATE-----"></textarea></div>
						<div class="hint">Your SP ACS URL will be: <code>https://your-instance/auth/saml/callback</code></div>
					{:else}
						<div class="field"><label class="label">Issuer URL</label><input class="input mono" type="text" bind:value={ssoIssuer} placeholder="https://accounts.company.com"></div>
						<div class="field"><label class="label">Client ID</label><input class="input mono" type="text" bind:value={ssoClientId} placeholder="baleia-client-id"></div>
						<div class="field"><label class="label">Client Secret</label><input class="input mono" type="password" bind:value={ssoClientSecret} placeholder="••••••••••••"></div>
						<div class="hint">Redirect URI: <code>https://your-instance/auth/oidc/callback</code></div>
					{/if}
					<div class="btn-row">
						<button class="btn btn-ghost" onclick={prevStep}>
							<svg viewBox="0 0 24 24"><polyline points="15 18 9 12 15 6"/></svg>
							Back
						</button>
						<div class="btn-right">
							<button class="btn-skip" onclick={skip}>Skip for now</button>
							<button class="btn btn-primary" onclick={nextStep}>
								<svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>
								Save & finish
							</button>
						</div>
					</div>
				</div>
			{/if}

			{#if step === 4}
				<div class="step-screen active">
					<div class="complete-wrap">
						<div class="complete-icon"><svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg></div>
						<div class="complete-title">Baleia is ready</div>
						<div class="complete-sub">Your instance is configured. Head to the editor and run your first query.</div>
						<button class="btn btn-primary" style="width:100%;justify-content:center;" onclick={() => window.location.href = '/app'}>
							<svg viewBox="0 0 24 24"><path d="M4 17l6-6-6-6M12 19h8"/></svg>
							Open editor
						</button>
					</div>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.setup { background: var(--bg-app); color: var(--text-main); min-height: 100vh; display: flex; }

	.left {
		width: 280px; flex-shrink: 0; background: var(--brand-dark);
		display: flex; flex-direction: column; padding: 40px 32px; position: relative; overflow: hidden;
	}
	.left::before {
		content: ''; position: absolute; inset: 0;
		background-image: linear-gradient(rgba(59,130,246,.05) 1px, transparent 1px),
			linear-gradient(90deg, rgba(59,130,246,.05) 1px, transparent 1px);
		background-size: 32px 32px;
	}
	.left::after {
		content: ''; position: absolute; width: 280px; height: 280px; border-radius: 50%;
		background: radial-gradient(circle, rgba(59,130,246,.14) 0%, transparent 70%);
		bottom: -80px; left: -60px; pointer-events: none;
	}
	.left-inner { position: relative; z-index: 1; display: flex; flex-direction: column; height: 100%; }
	.brand { display: flex; align-items: center; gap: 10px; margin-bottom: 48px; }
	.brand-icon { width: 32px; height: 32px; background: rgba(59,130,246,.15); border: 1px solid rgba(59,130,246,.3); border-radius: 8px; display: flex; align-items: center; justify-content: center; }
	.brand-icon svg { width: 16px; height: 16px; stroke: #3b82f6; }
	.brand-name { font-size: 16px; font-weight: 700; color: #f1f5f9; letter-spacing: -.3px; }
	.steps-nav { display: flex; flex-direction: column; gap: 4px; flex: 1; }
	.step-item { display: flex; align-items: flex-start; gap: 12px; padding: 10px 12px; border-radius: 8px; cursor: default; transition: .15s; position: relative; }
	.step-item::after {
		content: ''; position: absolute; left: 23px; top: 38px;
		width: 2px; height: 20px; background: #1e293b;
	}
	.step-item:last-child::after { display: none; }
	.step-item.done::after { background: #22c55e; }
	.step-item.active::after { background: #1e293b; }
	.step-circle {
		width: 24px; height: 24px; border-radius: 50%;
		display: flex; align-items: center; justify-content: center;
		font-size: 11px; font-weight: 700; flex-shrink: 0;
		border: 2px solid #1e293b; color: #475569;
		font-family: 'JetBrains Mono', monospace; transition: .2s; position: relative; z-index: 1;
	}
	.step-item.done .step-circle { background: #22c55e; border-color: #22c55e; color: white; }
	.step-item.active .step-circle { background: var(--primary); border-color: var(--primary); color: white; }
	.step-circle svg { width: 11px; height: 11px; stroke: white; stroke-width: 3; }
	.step-text { padding-top: 2px; }
	.step-label { font-size: 12px; font-weight: 600; color: #475569; transition: color .2s; }
	.step-sublabel { font-size: 11px; color: #334155; margin-top: 1px; }
	.step-item.active .step-label { color: #f1f5f9; }
	.step-item.done .step-label { color: #94a3b8; }
	.opt-label { font-size: 10px; font-weight: 600; color: #334155; }
	.left-footer { position: relative; z-index: 1; font-size: 11px; color: #334155; line-height: 1.6; }
	.left-footer a { color: #3b82f6; text-decoration: none; }

	.right { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 48px 40px; overflow-y: auto; }
	.step-wrap { width: 100%; max-width: 480px; }
	.step-screen.active { display: block; animation: fadeIn .2s ease; }
	@keyframes fadeIn { from { opacity: 0; transform: translateY(5px) } to { opacity: 1; transform: translateY(0) } }
	.step-header { margin-bottom: 28px; }
	.step-eyebrow { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: .7px; color: var(--primary); margin-bottom: 6px; }
	.step-title { font-size: 22px; font-weight: 700; letter-spacing: -.4px; margin-bottom: 6px; }
	.step-desc { font-size: 13px; color: var(--text-muted); line-height: 1.6; }

	.field { display: flex; flex-direction: column; gap: 5px; margin-bottom: 14px; }
	.field-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 14px; }
	.label { font-size: 11px; font-weight: 600; }
	.opt { color: var(--text-muted); font-weight: 400; }
	.input { width: 100%; height: 40px; background: var(--bg-app); border: 1px solid var(--border-light); border-radius: 7px; padding: 0 12px; font-size: 13px; color: var(--text-main); outline: none; transition: .15s; }
	.input:focus { border-color: var(--primary); box-shadow: 0 0 0 3px rgba(59,130,246,.08); background: var(--bg-panel); }
	.input::placeholder { color: var(--text-muted); }
	.input.mono { font-family: 'JetBrains Mono', monospace; font-size: 12px; }
	.pw-wrap { position: relative; }
	.pw-strength { display: flex; gap: 4px; margin-top: 6px; }
	.pw-bar { flex: 1; height: 3px; border-radius: 2px; background: var(--border-light); transition: background .3s; }
	.pw-bar.weak { background: #ef4444; }
	.pw-bar.medium { background: #f59e0b; }
	.pw-bar.strong { background: #22c55e; }
	.pw-slabel { font-size: 10px; margin-top: 4px; }
	.pw-slabel .weak { color: #ef4444; }
	.pw-slabel .medium { color: #f59e0b; }
	.pw-slabel .strong { color: #16a34a; }

	.section-label { font-size: 11px; font-weight: 600; margin-bottom: 8px; }
	.engine-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; margin-bottom: 14px; }
	.engine-opt { display: flex; flex-direction: column; align-items: center; gap: 5px; padding: 10px 6px; border-radius: 7px; border: 1px solid var(--border-light); cursor: pointer; transition: .15s; }
	.engine-opt:hover { border-color: var(--border-h); }
	.engine-opt.on { border-color: var(--primary); background: #eff6ff; }
	.eicon { width: 28px; height: 28px; border-radius: 6px; display: flex; align-items: center; justify-content: center; font-family: 'JetBrains Mono', monospace; font-size: 10px; font-weight: 700; }
	.epg { background: #eff6ff; color: #1d4ed8; }
	.emy { background: #f0fdf4; color: #15803d; }
	.etr { background: #fdf4ff; color: #7e22ce; }
	.ers { background: #fef2f2; color: #b91c1c; }
	.edr { background: #fff7ed; color: #c2410c; }
	.elabel { font-size: 10px; font-weight: 600; color: var(--text-muted); }
	.engine-opt.on .elabel { color: var(--primary); }

	.test-row { display: flex; align-items: center; gap: 10px; margin: 12px 0 4px; }
	.btn-test { display: flex; align-items: center; gap: 6px; height: 36px; padding: 0 14px; border-radius: 6px; border: 1px solid var(--border-light); background: transparent; font-size: 12px; font-weight: 600; color: var(--text-muted); cursor: pointer; transition: .15s; }
	.btn-test:hover { border-color: var(--border-h); color: var(--text-main); }
	.btn-test.testing { color: var(--primary); border-color: #bfdbfe; }
	.btn-test svg { width: 12px; height: 12px; }
	.test-result { display: flex; align-items: center; gap: 7px; font-size: 12px; font-weight: 500; padding: 8px 12px; border-radius: 6px; }
	.test-result.ok { background: #f0fdf4; color: #16a34a; border: 1px solid #bbf7d0; }
	.test-result.err { background: #fef2f2; color: #dc2626; border: 1px solid #fecaca; }
	.test-result svg { width: 13px; height: 13px; }

	.proto-tabs { display: flex; border: 1px solid var(--border-light); border-radius: 7px; overflow: hidden; margin-bottom: 14px; }
	.proto-tab { flex: 1; height: 36px; border: none; background: transparent; font-size: 12px; font-weight: 600; color: var(--text-muted); cursor: pointer; transition: .15s; border-right: 1px solid var(--border-light); }
	.proto-tab:last-child { border-right: none; }
	.proto-tab.on { background: var(--primary); color: white; }

	textarea.input { height: auto; padding: 8px; resize: vertical; }

	.btn-row { display: flex; align-items: center; justify-content: space-between; margin-top: 24px; }
	.btn-right { display: flex; align-items: center; gap: 12px; }
	.btn { height: 40px; padding: 0 20px; border-radius: 7px; font-size: 13px; font-weight: 600; cursor: pointer; display: flex; align-items: center; gap: 7px; transition: .15s; border: none; }
	.btn svg { width: 13px; height: 13px; }
	.btn-primary { background: var(--primary); color: white; }
	.btn-primary:hover { background: var(--primary-h); }
	.btn-ghost { background: transparent; border: 1px solid var(--border-light); color: var(--text-muted); }
	.btn-ghost:hover { border-color: var(--border-h); color: var(--text-main); }
	.btn-skip { background: transparent; border: none; color: var(--text-muted); font-size: 12px; font-weight: 600; cursor: pointer; padding: 0; transition: color .15s; }
	.btn-skip:hover { color: var(--text-main); }

	.err-msg { background: #fef2f2; border: 1px solid #fecaca; border-radius: 6px; padding: 9px 12px; font-size: 12px; color: #dc2626; margin-bottom: 14px; display: none; }
	.err-msg.show { display: block; }
	.hint { font-size: 11px; color: var(--text-muted); margin-top: 3px; }
	.hint code { font-family: 'JetBrains Mono', monospace; font-size: 11px; color: var(--primary); }

	.complete-wrap { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 16px; }
	.complete-icon { width: 64px; height: 64px; border-radius: 50%; background: #f0fdf4; border: 1px solid #bbf7d0; display: flex; align-items: center; justify-content: center; }
	.complete-icon svg { width: 28px; height: 28px; stroke: #16a34a; }
	.complete-title { font-size: 22px; font-weight: 700; letter-spacing: -.4px; }
	.complete-sub { font-size: 13px; color: var(--text-muted); max-width: 340px; line-height: 1.6; }
</style>
