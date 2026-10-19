<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { client } from '$lib/api/client';
	import { PasswordInput } from '$lib/ui';

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
			error = m.reset_password_passwords_don_t_match();
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
			error = m.setup_connection_error_is_the_server_running();
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>{m.reset_password_reset_password_baleia()}</title>
	<meta name="referrer" content="no-referrer" />
</svelte:head>

<div
	class="bg-bg-app text-text-main min-h-screen flex items-center justify-center p-8 transition-colors duration-200"
>
	<div class="w-full max-w-[360px]">
		{#if ready}
			{#if !token || invalidLink}
				<div class="flex flex-col animate-[fadeIn_.2s_ease]">
					<div class="text-xl font-bold tracking-[-.4px] mb-1">
						{m.reset_password_reset_link_problem()}
					</div>
					<div class="text-[13px] text-text-muted mb-6 leading-[1.55]">
						{#if !token}
							{m.reset_password_this_reset_link_is_incomplete_request_a_ne()}
						{:else}
							This reset link is invalid or has expired.
						{/if}
					</div>
					<a
						href={resolve('/login')}
						class="w-full h-10 rounded-[7px] text-[13px] font-semibold flex items-center justify-center bg-primary text-white no-underline hover:bg-primary-hover"
					>
						{m.reset_password_request_a_new_link()}
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
					<div class="mb-3.5">
						<PasswordInput
							id="new-pw"
							label="New password"
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
					<div class="mb-3.5">
						<PasswordInput
							id="confirm-pw"
							label="Confirm password"
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
						{busy ? m.reset_password_updating() : m.reset_password_update_password()}
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
