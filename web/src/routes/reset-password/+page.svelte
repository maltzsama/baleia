<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { client } from '$lib/api/client';

	const MIN_LENGTH = 8;

	// Held in memory only: read once from the query string, then removed from the address bar.
	let token = $state('');
	let ready = $state(false);
	let newPassword = $state('');
	let confirmPassword = $state('');
	let error = $state('');
	let invalidLink = $state(false);
	let busy = $state(false);
	let strengthScore = $state(0);

	onMount(() => {
		token = new URLSearchParams(window.location.search).get('token') ?? '';
		history.replaceState(null, '', '/reset-password');
		ready = true;
	});

	function checkStrength(val: string) {
		let s = 0;
		if (val.length >= 8) s++;
		if (/[A-Z]/.test(val) && /[0-9]/.test(val)) s++;
		if (/[^A-Za-z0-9]/.test(val) && val.length >= 12) s++;
		strengthScore = s;
	}

	async function submit() {
		if (busy) return;
		if (newPassword.length < MIN_LENGTH) {
			error = `Password must be at least ${MIN_LENGTH} characters.`;
			return;
		}
		if (newPassword !== confirmPassword) {
			error = "Passwords don't match.";
			return;
		}
		busy = true;
		error = '';
		try {
			const { response } = await client.POST('/auth/password-reset/confirm', {
				body: { token, new_password: newPassword }
			});
			if (response.status === 204) {
				window.location.href = '/login?reset=1';
				return;
			}
			// The backend answers 401 for an unknown, expired or used token; 400/404/410 are treated the same.
			if ([400, 401, 404, 410].includes(response.status)) {
				invalidLink = true;
			} else if (response.status === 422) {
				error = `Password must be at least ${MIN_LENGTH} characters.`;
			} else {
				error = `Could not change the password (HTTP ${response.status}). Try again.`;
			}
		} catch {
			error = 'Connection error. Is the server running?';
		} finally {
			busy = false;
		}
	}

	const inputBase =
		'w-full h-10 bg-bg-app dark:bg-[#1c2128] border border-border rounded-[7px] px-3 text-[13px] text-text-main outline-none transition-all duration-150 placeholder:text-text-muted focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.1)] focus:bg-bg-panel';
</script>

<svelte:head>
	<title>Reset password · Baleia</title>
	<meta name="referrer" content="no-referrer" />
</svelte:head>

<div
	class="bg-bg-app text-text-main min-h-screen flex items-center justify-center p-8 transition-colors duration-200"
>
	<div class="w-full max-w-[360px]">
		{#if ready}
			{#if !token || invalidLink}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Reset link problem</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						{#if !token}
							This reset link is incomplete. Request a new one.
						{:else}
							This reset link is invalid or has expired.
						{/if}
					</div>
					<a
						href={resolve('/login')}
						class="w-full h-10 rounded-[7px] text-[13px] font-semibold flex items-center justify-center bg-primary text-white no-underline hover:bg-primary-hover"
					>
						Request a new link
					</a>
				</div>
			{:else}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">Set new password</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						Choose a strong password for your account.
					</div>
					{#if error}
						<div
							class="bg-red-50 border border-red-200 rounded-md py-[9px] px-3 text-xs text-red-600 mb-3.5"
						>
							{error}
						</div>
					{/if}
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold" for="new-pw">New password</label>
						<input
							id="new-pw"
							class={inputBase}
							type="password"
							autocomplete="new-password"
							bind:value={newPassword}
							placeholder="••••••••"
							oninput={(e) => checkStrength((e.target as HTMLInputElement).value)}
							onkeydown={(e) => e.key === 'Enter' && submit()}
						/>
						<div class="flex gap-1 mt-1.5">
							<div
								class="flex-1 h-[3px] rounded-sm transition-colors duration-300
								{strengthScore >= 3 ? 'bg-green-500' : strengthScore >= 1 ? 'bg-red-500' : 'bg-border'}"
							></div>
							<div
								class="flex-1 h-[3px] rounded-sm transition-colors duration-300
								{strengthScore >= 3 ? 'bg-green-500' : strengthScore >= 2 ? 'bg-amber-500' : 'bg-border'}"
							></div>
							<div
								class="flex-1 h-[3px] rounded-sm transition-colors duration-300
								{strengthScore >= 3 ? 'bg-green-500' : 'bg-border'}"
							></div>
						</div>
						<div class="text-[10px] mt-1">
							{#if strengthScore === 1}<span class="text-red-500">Weak</span>
							{:else if strengthScore === 2}<span class="text-amber-500">Fair</span>
							{:else if strengthScore >= 3}<span class="text-green-600">Strong</span>
							{/if}
						</div>
					</div>
					<div class="flex flex-col gap-[5px] mb-3.5">
						<label class="text-[11px] font-semibold" for="confirm-pw">Confirm password</label>
						<input
							id="confirm-pw"
							class={inputBase}
							type="password"
							autocomplete="new-password"
							bind:value={confirmPassword}
							placeholder="••••••••"
							onkeydown={(e) => e.key === 'Enter' && submit()}
						/>
					</div>
					<button
						onclick={submit}
						disabled={busy}
						class="w-full h-10 border-none rounded-[7px] text-[13px] font-semibold cursor-pointer transition-colors duration-150 flex items-center justify-center gap-[7px] bg-primary text-white mb-2.5
							hover:enabled:bg-primary-hover disabled:bg-blue-300 disabled:cursor-not-allowed"
					>
						{busy ? 'Updating…' : 'Update password'}
					</button>
				</div>
			{/if}
		{/if}
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
</style>
