<script lang="ts">
	import { client, setAccessToken, setRefreshToken } from '$lib/api/client';
	import ConnectionWizard from '$lib/connections/ConnectionWizard.svelte';

	let step = $state(1);
	let firstName = $state('');
	let lastName = $state('');
	let email = $state('');
	let password = $state('');
	let confirmPass = $state('');
	let strengthScore = $state(0);
	let error = $state('');
	let busy = $state(false);

	const steps = [
		{ label: 'Admin account', sub: 'Name, email & password' },
		{ label: 'First connection', sub: 'Connect a datasource', optional: true },
		{ label: 'Done', sub: 'Open the editor' }
	];

	const inputBase =
		'w-full h-10 bg-bg-app border border-border rounded-[7px] px-3 text-[13px] text-text-main outline-none transition-all duration-150 placeholder:text-slate-600 dark:text-slate-300 focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] focus:bg-bg-panel';

	function checkStrength(val: string) {
		let s = 0;
		if (val.length >= 8) s++;
		if (/[A-Z]/.test(val) && /[0-9]/.test(val)) s++;
		if (/[^A-Za-z0-9]/.test(val) && val.length >= 12) s++;
		strengthScore = s;
	}

	async function createAdmin() {
		error = '';
		if (!firstName || !email || !password || password !== confirmPass) {
			error = 'Fill in the required fields and make sure the passwords match.';
			return;
		}
		busy = true;
		try {
			const { data, error: err } = await client.POST('/setup/admin', {
				body: { name: `${firstName} ${lastName}`.trim(), email, password }
			});
			if (err || !data) {
				error = 'Failed to create the admin account.';
				return;
			}
			setAccessToken(data.token);
			setRefreshToken(data.refresh_token);
			step = 2;
		} catch {
			error = 'Connection error. Is the server running?';
		} finally {
			busy = false;
		}
	}
</script>

<div class="bg-bg-app text-text-main min-h-screen flex">
	<div
		class="w-[280px] shrink-0 bg-brand-dark flex flex-col p-10 relative overflow-hidden
		before:content-[''] before:absolute before:inset-0
		before:[background-image:linear-gradient(rgba(59,130,246,.05)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,.05)_1px,transparent_1px)]
		before:bg-[length:32px_32px]
		after:content-[''] after:absolute after:w-[280px] after:h-[280px] after:rounded-full
		after:[background:radial-gradient(circle,rgba(59,130,246,.14)_0%,transparent_70%)]
		after:-bottom-20 after:-left-[60px] after:pointer-events-none"
	>
		<div class="relative z-[1] flex flex-col h-full">
			<div class="flex items-center gap-2.5 mb-12">
				<div
					class="w-8 h-8 bg-primary/15 border border-primary/30 rounded-lg flex items-center justify-center"
				>
					<svg viewBox="0 0 24 24" class="w-4 h-4 stroke-primary"
						><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" /></svg
					>
				</div>
				<span class="text-base font-bold text-slate-100 tracking-[-.3px]">Baleia</span>
			</div>

			<div class="flex flex-col gap-1 flex-1">
				{#each steps as s, i (s.label)}
					{@const n = i + 1}
					{@const done = step > n}
					{@const active = step === n}
					<div class="flex items-start gap-3 py-2.5 px-3 rounded-lg">
						<div
							class="w-6 h-6 rounded-full flex items-center justify-center text-[11px] font-bold shrink-0 border-2 font-mono transition-all duration-200 relative z-[1]
							{done
								? 'bg-green-500 border-green-500 text-white'
								: active
									? 'bg-primary border-primary text-white'
									: 'border-slate-800 text-slate-600'}"
						>
							{#if done}
								<svg viewBox="0 0 24 24" class="w-[11px] h-[11px] stroke-white" stroke-width="3"
									><polyline points="20 6 9 17 4 12" /></svg
								>
							{:else}
								{n}
							{/if}
						</div>
						<div class="pt-0.5">
							<div
								class="text-xs font-semibold transition-colors duration-200
								{active ? 'text-slate-100' : done ? 'text-slate-400' : 'text-slate-600'}"
							>
								{s.label}{#if s.optional}
									<span class="text-[10px] font-semibold text-slate-700">(optional)</span>{/if}
							</div>
							<div class="text-[11px] text-slate-700 mt-px">{s.sub}</div>
						</div>
					</div>
				{/each}
			</div>

			<div class="relative z-[1] text-[11px] text-slate-700 leading-relaxed">
				Baleia is open-source.<br />
				<a href="https://github.com" target="_blank" class="text-primary no-underline"
					>github.com/baleia</a
				>
			</div>
		</div>
	</div>

	<div class="flex-1 flex flex-col items-center justify-center py-12 px-10 overflow-y-auto">
		{#if step === 1}
			<div class="w-full max-w-[480px] animate-[fadeIn_.2s_ease]">
				<div class="mb-7">
					<div class="text-[11px] font-bold uppercase tracking-[.7px] text-primary mb-1.5">
						Step 1 of 3
					</div>
					<div class="text-[22px] font-bold tracking-[-.4px] mb-1.5">Create admin account</div>
					<div class="text-[13px] text-slate-600 dark:text-slate-300 leading-[1.6]">
						This will be the first administrator of your Baleia instance.
					</div>
				</div>
				{#if error}
					<div
						class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5"
					>
						{error}
					</div>
				{/if}
				<div class="grid grid-cols-2 gap-3 mb-3.5">
					<div class="flex flex-col gap-[5px]">
						<label class="text-[11px] font-semibold" for="setup-first">First name</label>
						<input
							id="setup-first"
							class={inputBase}
							type="text"
							bind:value={firstName}
							placeholder="John"
						/>
					</div>
					<div class="flex flex-col gap-[5px]">
						<label class="text-[11px] font-semibold" for="setup-last">Last name</label>
						<input
							id="setup-last"
							class={inputBase}
							type="text"
							bind:value={lastName}
							placeholder="Doe"
						/>
					</div>
				</div>
				<div class="flex flex-col gap-[5px] mb-3.5">
					<label class="text-[11px] font-semibold" for="setup-email">Email</label>
					<input
						id="setup-email"
						class="{inputBase} font-mono text-xs"
						type="email"
						bind:value={email}
						placeholder="admin@company.com"
					/>
				</div>
				<div class="flex flex-col gap-[5px] mb-3.5">
					<label class="text-[11px] font-semibold" for="setup-password">Password</label>
					<input
						id="setup-password"
						class={inputBase}
						type="password"
						bind:value={password}
						placeholder="••••••••"
						oninput={(e) => checkStrength((e.target as HTMLInputElement).value)}
					/>
					<div class="flex gap-1 mt-1.5">
						{#each [1, 2, 3] as bar (bar)}
							<div
								class="flex-1 h-[3px] rounded-sm transition-colors duration-300 {strengthScore >=
								bar
									? bar === 3
										? 'bg-green-500'
										: bar === 2
											? 'bg-amber-500'
											: 'bg-red-500'
									: 'bg-border'}"
							></div>
						{/each}
					</div>
				</div>
				<div class="flex flex-col gap-[5px] mb-3.5">
					<label class="text-[11px] font-semibold" for="setup-confirm">Confirm password</label>
					<input
						id="setup-confirm"
						class={inputBase}
						type="password"
						bind:value={confirmPass}
						placeholder="••••••••"
					/>
				</div>
				<div class="flex items-center justify-end mt-6">
					<button
						onclick={createAdmin}
						disabled={busy}
						class="h-10 px-5 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center gap-[7px] transition-colors duration-150 border-none bg-primary text-white hover:bg-primary-hover disabled:opacity-50"
					>
						{busy ? 'Creating…' : 'Continue'}
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"
							><polyline points="9 18 15 12 9 6" /></svg
						>
					</button>
				</div>
			</div>
		{:else if step === 2}
			<div class="w-full max-w-[920px] animate-[fadeIn_.2s_ease]">
				<div class="mb-6 text-center">
					<div class="text-[11px] font-bold uppercase tracking-[.7px] text-primary mb-1.5">
						Step 2 of 3 — Optional
					</div>
					<div class="text-[22px] font-bold tracking-[-.4px] mb-1.5">Connect a datasource</div>
					<div class="text-[13px] text-slate-600 dark:text-slate-300 leading-[1.6]">
						Add your first connection. You can skip this and connect later.
					</div>
				</div>
				<div class="rounded-xl border border-border bg-bg-panel p-5">
					<ConnectionWizard ondone={() => (step = 3)} />
				</div>
				<div class="mt-4 text-center">
					<button
						onclick={() => (step = 3)}
						class="bg-transparent border-none text-slate-600 dark:text-slate-300 text-xs font-semibold cursor-pointer p-0 transition-colors duration-150 hover:text-text-main"
					>
						Skip for now
					</button>
				</div>
			</div>
		{:else}
			<div class="w-full max-w-[480px] animate-[fadeIn_.2s_ease]">
				<div class="flex flex-col items-center text-center gap-4">
					<div
						class="w-16 h-16 rounded-full bg-green-50 border border-green-200 flex items-center justify-center"
					>
						<svg viewBox="0 0 24 24" class="w-7 h-7 stroke-green-600"
							><polyline points="20 6 9 17 4 12" /></svg
						>
					</div>
					<div class="text-[22px] font-bold tracking-[-.4px]">Baleia is ready</div>
					<div class="text-[13px] text-slate-600 dark:text-slate-300 max-w-[340px] leading-[1.6]">
						Your instance is configured. Head to the editor and run your first query.
					</div>
					<button
						onclick={() => (window.location.href = '/sql')}
						class="w-full h-10 rounded-[7px] text-[13px] font-semibold cursor-pointer flex items-center justify-center gap-[7px] transition-colors duration-150 border-none bg-primary text-white hover:bg-primary-hover"
					>
						<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"
							><path d="M4 17l6-6-6-6M12 19h8" /></svg
						>
						Open editor
					</button>
				</div>
			</div>
		{/if}
	</div>
</div>

<style>
	@keyframes fadeIn {
		from {
			opacity: 0;
			transform: translateY(5px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}
</style>
