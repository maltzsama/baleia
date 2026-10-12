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
	let connUrl = $state('http://polaris:8181');
	let connUser = $state('');
	let connPass = $state('');
	let testStatus = $state<'idle' | 'testing' | 'ok' | 'err'>('idle');
	let testMsg = $state('');
	let verifiedId = $state('');

	let ssoIssuer = $state('');
	let ssoClientId = $state('');
	let ssoClientSecret = $state('');
	let ssoIdpUrl = $state('');
	let ssoEntityId = $state('');
	let ssoCert = $state('');

	const apiBase = '/api/v1';
	const token = () => localStorage.getItem('token') || '';

	function isIcebergType() { return ['polaris', 'nessie', 'iceberg'].includes(engine); }

	function checkStrength(val: string) {
		let s = 0;
		if (val.length >= 8) s++;
		if (/[A-Z]/.test(val) && /[0-9]/.test(val)) s++;
		if (/[^A-Za-z0-9]/.test(val) && val.length >= 12) s++;
		strengthScore = s;
	}

	function buildConnBody() {
		const body: any = { name: connName, type: engine, url: connUrl, default_readonly: true };
		if (isIcebergType()) {
			const creds = { client_id: connUser, client_secret: connPass };
			body.polaris_credentials = creds;
			body.credentials = creds;
		}
		return body;
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
		if (step === 2 && connName) {
			if (!verifiedId) {
				try {
					await fetch(`${apiBase}/catalog-connections`, {
						method: 'POST',
						headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token()}` },
						body: JSON.stringify(buildConnBody())
					});
				} catch { /* optional */ }
			}
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

	function selectEngine(code: string) {
		engine = code;
	}

	async function testConnection() {
		if (!connName || !connUrl || !connUser || !connPass) {
			testStatus = 'err';
			testMsg = 'Fill in all fields first.';
			return;
		}
		testStatus = 'testing';
		testMsg = '';
		verifiedId = '';
		try {
			const saveRes = await fetch(`${apiBase}/catalog-connections`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token()}` },
				body: JSON.stringify(buildConnBody())
			});
			if (!saveRes.ok) {
				const body = await saveRes.json().catch(() => ({}));
				testStatus = 'err';
				testMsg = body.message || body.error || `Save failed (HTTP ${saveRes.status})`;
				return;
			}
			const saved = await saveRes.json();
			const testId = saved.id;
			const testRes = await fetch(`${apiBase}/catalog-connections/${testId}/test`, {
				method: 'POST', headers: { 'Authorization': `Bearer ${token()}` }
			});
			const testResult = await testRes.json();
			if (testResult.success) {
				testStatus = 'ok';
				testMsg = `Connection successful — ${testResult.latency_ms}ms`;
				verifiedId = testId;
			} else {
				testStatus = 'err';
				testMsg = testResult.error || 'Connection test failed';
				await fetch(`${apiBase}/catalog-connections/${testId}`, {
					method: 'DELETE', headers: { 'Authorization': `Bearer ${token()}` }
				});
			}
		} catch (e: any) {
			testStatus = 'err';
			testMsg = e.message || 'Connection error';
		}
	}

	let engines = [
		{ code: 'polaris', label: 'Polaris', cls: 'bg-blue-50 text-blue-700' },
		{ code: 'nessie', label: 'Nessie', cls: 'bg-green-50 text-green-700' },
		{ code: 'iceberg', label: 'Iceberg', cls: 'bg-fuchsia-50 text-purple-700' },
		{ code: 'glue', label: 'Glue', cls: 'bg-orange-50 text-orange-700' },
		{ code: 'hive', label: 'Hive', cls: 'bg-red-50 text-red-700' },
	];

	const inputBase = "w-full h-10 bg-bg-app border border-border rounded-[7px] px-3 text-[13px] text-text-main outline-none transition-all duration-150 placeholder:text-text-muted focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] focus:bg-bg-panel";
</script>

<div class="bg-bg-app text-text-main min-h-screen flex">

	<!-- Left steps nav -->
	<div class="w-[280px] shrink-0 bg-brand-dark flex flex-col p-10 relative overflow-hidden
		before:content-[''] before:absolute before:inset-0
		before:[background-image:linear-gradient(rgba(59,130,246,.05)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,.05)_1px,transparent_1px)]
		before:bg-[length:32px_32px]
		after:content-[''] after:absolute after:w-[280px] after:h-[280px] after:rounded-full
		after:[background:radial-gradient(circle,rgba(59,130,246,.14)_0%,transparent_70%)]
		after:-bottom-20 after:-left-[60px] after:pointer-events-none">

		<div class="relative z-[1] flex flex-col h-full">
			<div class="flex items-center gap-2.5 mb-12">
				<div class="w-8 h-8 bg-primary/15 border border-primary/30 rounded-lg flex items-center justify-center">
					<svg viewBox="0 0 24 24" class="w-4 h-4 stroke-primary"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>
				</div>
				<span class="text-base font-bold text-slate-100 tracking-[-.3px]">Baleia</span>
			</div>

			<div class="flex flex-col gap-1 flex-1">
				{#each [
					{ label: 'Admin account', sub: 'Name, email & password' },
					{ label: 'First connection', sub: 'Connect a datasource', opt: true },
					{ label: 'SSO', sub: 'SAML or OIDC', opt: true }
				] as s, i}
					{@const n = i + 1}
					{@const done = step > n}
					{@const active = step === n}
					<div class="flex items-start gap-3 py-2.5 px-3 rounded-lg relative
						{i < 2 ? `after:content-[''] after:absolute after:left-[23px] after:top-[38px] after:w-0.5 after:h-5 ${done ? 'after:bg-green-500' : 'after:bg-slate-800'}` : ''}">
						<div class="w-6 h-6 rounded-full flex items-center justify-center text-[11px] font-bold shrink-0 border-2 font-mono transition-all duration-200 relative z-[1]
							{done ? 'bg-green-500 border-green-500 text-white' : active ? 'bg-primary border-primary text-white' : 'border-slate-800 text-slate-600'}">
							{#if done}
								<svg viewBox="0 0 24 24" class="w-[11px] h-[11px] stroke-white" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>
							{:else}
								{n}
							{/if}
						</div>
						<div class="pt-0.5">
							<div class="text-xs font-semibold transition-colors duration-200
								{active ? 'text-slate-100' : done ? 'text-slate-400' : 'text-slate-600'}">
								{s.label}{#if s.opt} <span class="text-[10px] font-semibold text-slate-700">(optional)</span>{/if}
							</div>
							<div class="text-[11px] text-slate-700 mt-px">{s.sub}</div>
						</div>
					</div>
				{/each}
			</div>

			<div class="relative z-[1] text-[11px] text-slate-700 leading-relaxed">
				Baleia is open-source.<br>
				<a href="https://github.com" target="_blank" class="text-primary no-underline">github.com/baleia</a>
			</div>
		</div>
	</div>

	<!-- Right step content -->
	<div class="flex-1 flex flex-col items-center justify-center py-12 px-10 overflow-y-auto">
		<div class="w-full max-w-[480px]">

			{#if step === 1}
				<div class="animate-[fadeIn_.2s_ease]">
					<div class="mb-7">
						<div class="text-[11px] font-bold uppercase tracking-[.7px] text-primary mb-1.5">Step 1 of 3</div>
						<div class="text-[22px] font-bold tracking-[-.4px] mb-1.5">Create admin account</div>
						<div class="text-[13px] text-text-muted leading-[1.6]">This will be the first administrator of your Baleia instance.</div>
					</div>
					{#if error}<div class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5">{error}</div>{/if}
					<div class="grid grid-cols-2 gap-3 mb-3.5">
						<div class="flex flex-col gap-[5px]"><label class="text-[11px] font-semibold">First name</label><input class={inputBase} type="text" bind:value={firstName} placeholder="John"></div>
						<div class="flex flex-col gap-[5px]"><label class="text-[11px] font-semibold">Last name</label><input class={inputBase} type="text" bind:value={lastName} placeholder="Doe"></div>
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Email</label>
						<input class="{inputBase} font-mono text-xs" type="email" bind:value={email} placeholder="admin@company.com">
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Password</label>
						<input class={inputBase} type="password" bind:value={password} placeholder="••••••••"
							oninput={(e) => checkStrength((e.target as HTMLInputElement).value)}>
						<div class="flex gap-1 mt-1.5">
							<div class="flex-1 h-[3px] rounded-sm transition-colors duration-300 {strengthScore >= 3 ? 'bg-green-500' : strengthScore >= 1 ? 'bg-red-500' : 'bg-border'}"></div>
							<div class="flex-1 h-[3px] rounded-sm transition-colors duration-300 {strengthScore >= 3 ? 'bg-green-500' : strengthScore >= 2 ? 'bg-amber-500' : 'bg-border'}"></div>
							<div class="flex-1 h-[3px] rounded-sm transition-colors duration-300 {strengthScore >= 3 ? 'bg-green-500' : 'bg-border'}"></div>
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
						<input class={inputBase} type="password" bind:value={confirmPass} placeholder="••••••••">
					</div>
					<div class="flex items-center justify-between mt-6">
						<span></span>
						<button onclick={nextStep} class="h-10 px-5 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center gap-[7px] transition-colors duration-150 border-none bg-primary text-white hover:bg-primary-hover">
							Continue
							<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="9 18 15 12 9 6"/></svg>
						</button>
					</div>
				</div>
			{/if}

			{#if step === 2}
				<div class="animate-[fadeIn_.2s_ease]">
					<div class="mb-7">
						<div class="text-[11px] font-bold uppercase tracking-[.7px] text-primary mb-1.5">Step 2 of 3 — Optional</div>
						<div class="text-[22px] font-bold tracking-[-.4px] mb-1.5">Connect a datasource</div>
						<div class="text-[13px] text-text-muted leading-[1.6]">Add your first catalog connection. You can skip this and connect later.</div>
					</div>
					{#if error}<div class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5">{error}</div>{/if}
					<div class="text-[11px] font-semibold mb-2">Catalog type</div>
					<div class="grid grid-cols-3 gap-2 mb-3.5">
						{#each engines as e}
							<div
								onclick={() => selectEngine(e.code)}
								class="flex flex-col items-center gap-[5px] py-2.5 px-1.5 rounded-[7px] border cursor-pointer transition-all duration-150
									{engine === e.code ? 'border-primary bg-blue-50' : 'border-border hover:border-border-hover'}">
								<div class="w-7 h-7 rounded-md flex items-center justify-center font-mono text-[10px] font-bold {e.cls}">{e.code.slice(0,2).toUpperCase()}</div>
								<span class="text-[10px] font-semibold {engine === e.code ? 'text-primary' : 'text-text-muted'}">{e.label}</span>
							</div>
						{/each}
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold">Connection name</label>
						<input class={inputBase} type="text" bind:value={connName} placeholder="e.g. analytics-prod">
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5"><label class="text-[11px] font-semibold">URL</label><input class="{inputBase} font-mono text-xs" type="text" bind:value={connUrl} placeholder="http://polaris:8181"></div>
					<div class="grid grid-cols-2 gap-3 mb-3.5">
						<div class="flex flex-col gap-[5px]"><label class="text-[11px] font-semibold">Username</label><input class={inputBase} type="text" bind:value={connUser} placeholder="user"></div>
						<div class="flex flex-col gap-[5px]"><label class="text-[11px] font-semibold">Password</label><input class={inputBase} type="password" bind:value={connPass} placeholder="••••••••"></div>
					</div>
					<div class="flex items-center gap-2.5 my-3 mb-1">
						<button
							onclick={testConnection}
							disabled={testStatus === 'testing' || !connName || !connUrl || !connUser || !connPass}
							class="flex items-center gap-1.5 h-9 px-3.5 rounded-md border text-xs font-semibold cursor-pointer transition-all duration-150
								{testStatus === 'testing' ? 'text-primary border-blue-200' : 'border-border text-text-muted hover:border-border-hover hover:text-text-main'} bg-transparent
								disabled:opacity-40 disabled:cursor-not-allowed">
							<svg viewBox="0 0 24 24" class="w-3 h-3"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
							{testStatus === 'testing' ? 'Testing...' : 'Test connection'}
						</button>
						{#if testStatus === 'ok'}
							<div class="flex items-center gap-[7px] text-xs font-medium py-2 px-3 rounded-md bg-green-50 text-green-600 border border-green-200">
								<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="20 6 9 17 4 12"/></svg> {testMsg}
							</div>
						{:else if testStatus === 'err'}
							<div class="flex items-center gap-[7px] text-xs font-medium py-2 px-3 rounded-md bg-red-50 text-red-600 border border-red-200">
								<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg> {testMsg}
							</div>
						{/if}
					</div>
					<div class="flex items-center justify-between mt-6">
						<button onclick={prevStep} class="h-10 px-5 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center gap-[7px] transition-all duration-150 bg-transparent border border-border text-text-muted hover:border-border-hover hover:text-text-main">
							<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="15 18 9 12 15 6"/></svg>
							Back
						</button>
						<div class="flex items-center gap-3">
							<button onclick={skip} class="bg-transparent border-none text-text-muted text-xs font-semibold cursor-pointer p-0 transition-colors duration-150 hover:text-text-main">Skip for now</button>
							<button onclick={nextStep} class="h-10 px-5 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center gap-[7px] transition-colors duration-150 border-none bg-primary text-white hover:bg-primary-hover">
								Continue
								<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="9 18 15 12 9 6"/></svg>
							</button>
						</div>
					</div>
				</div>
			{/if}

			{#if step === 3}
				<div class="animate-[fadeIn_.2s_ease]">
					<div class="mb-7">
						<div class="text-[11px] font-bold uppercase tracking-[.7px] text-primary mb-1.5">Step 3 of 3 — Optional</div>
						<div class="text-[22px] font-bold tracking-[-.4px] mb-1.5">Configure SSO</div>
						<div class="text-[13px] text-text-muted leading-[1.6]">Let your team sign in through your identity provider.</div>
					</div>
					<div class="flex border border-border rounded-[7px] overflow-hidden mb-3.5">
						<button onclick={() => proto = 'saml'}
							class="flex-1 h-9 border-none text-xs font-semibold cursor-pointer transition-all duration-150 border-r border-border last:border-r-0
								{proto === 'saml' ? 'bg-primary text-white' : 'bg-transparent text-text-muted'}">
							SAML 2.0
						</button>
						<button onclick={() => proto = 'oidc'}
							class="flex-1 h-9 border-none text-xs font-semibold cursor-pointer transition-all duration-150
								{proto === 'oidc' ? 'bg-primary text-white' : 'bg-transparent text-text-muted'}">
							OIDC
						</button>
					</div>
					{#if proto === 'saml'}
						<div class="flex flex-col gap-[5px] mb-3.5">
							<label class="text-[11px] font-semibold">IdP SSO URL</label>
							<input class="{inputBase} font-mono text-xs" type="text" bind:value={ssoIdpUrl} placeholder="https://idp.company.com/sso/saml">
						</div>
						<div class="flex flex-col gap-[5px] mb-3.5">
							<label class="text-[11px] font-semibold">IdP Entity ID</label>
							<input class="{inputBase} font-mono text-xs" type="text" bind:value={ssoEntityId} placeholder="https://idp.company.com/entity">
						</div>
						<div class="flex flex-col gap-[5px] mb-3.5">
							<label class="text-[11px] font-semibold">X.509 Certificate <span class="text-text-muted font-normal">(PEM)</span></label>
							<textarea class="{inputBase} h-auto py-2 resize-y" rows="3" bind:value={ssoCert} placeholder="-----BEGIN CERTIFICATE-----"></textarea>
						</div>
						<div class="text-[11px] text-text-muted mt-[3px]">
							Your SP ACS URL will be: <code class="font-mono text-[11px] text-primary">https://your-instance/auth/saml/callback</code>
						</div>
					{:else}
						<div class="flex flex-col gap-[5px] mb-3.5">
							<label class="text-[11px] font-semibold">Issuer URL</label>
							<input class="{inputBase} font-mono text-xs" type="text" bind:value={ssoIssuer} placeholder="https://accounts.company.com">
						</div>
						<div class="flex flex-col gap-[5px] mb-3.5">
							<label class="text-[11px] font-semibold">Client ID</label>
							<input class="{inputBase} font-mono text-xs" type="text" bind:value={ssoClientId} placeholder="baleia-client-id">
						</div>
						<div class="flex flex-col gap-[5px] mb-3.5">
							<label class="text-[11px] font-semibold">Client Secret</label>
							<input class="{inputBase} font-mono text-xs" type="password" bind:value={ssoClientSecret} placeholder="••••••••••••">
						</div>
						<div class="text-[11px] text-text-muted mt-[3px]">
							Redirect URI: <code class="font-mono text-[11px] text-primary">https://your-instance/auth/oidc/callback</code>
						</div>
					{/if}
					<div class="flex items-center justify-between mt-6">
						<button onclick={prevStep} class="h-10 px-5 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center gap-[7px] transition-all duration-150 bg-transparent border border-border text-text-muted hover:border-border-hover hover:text-text-main">
							<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="15 18 9 12 15 6"/></svg>
							Back
						</button>
						<div class="flex items-center gap-3">
							<button onclick={skip} class="bg-transparent border-none text-text-muted text-xs font-semibold cursor-pointer p-0 transition-colors duration-150 hover:text-text-main">Skip for now</button>
							<button onclick={nextStep} class="h-10 px-5 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center gap-[7px] transition-colors duration-150 border-none bg-primary text-white hover:bg-primary-hover">
								<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><polyline points="20 6 9 17 4 12"/></svg>
								Save & finish
							</button>
						</div>
					</div>
				</div>
			{/if}

			{#if step === 4}
				<div class="animate-[fadeIn_.2s_ease]">
					<div class="flex flex-col items-center text-center gap-4">
						<div class="w-16 h-16 rounded-full bg-green-50 border border-green-200 flex items-center justify-center">
							<svg viewBox="0 0 24 24" class="w-7 h-7 stroke-green-600"><polyline points="20 6 9 17 4 12"/></svg>
						</div>
						<div class="text-[22px] font-bold tracking-[-.4px]">Baleia is ready</div>
						<div class="text-[13px] text-text-muted max-w-[340px] leading-[1.6]">Your instance is configured. Head to the editor and run your first query.</div>
						<button onclick={() => window.location.href = '/sql'}
							class="w-full h-10 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center justify-center gap-[7px] transition-colors duration-150 border-none bg-primary text-white hover:bg-primary-hover">
							<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"><path d="M4 17l6-6-6-6M12 19h8"/></svg>
							Open editor
						</button>
					</div>
				</div>
			{/if}

		</div>
	</div>
</div>

<style>
	@keyframes fadeIn { from { opacity: 0; transform: translateY(5px) } to { opacity: 1; transform: translateY(0) } }
</style>