<script lang="ts">
	import { onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let saving = $state(false);
	let changingPassword = $state(false);
	let error = $state('');
	let successMsg = $state('');

	let name = $state('');
	let email = $state('');

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');

	onMount(async () => {
		await authStore.load();
		name = authStore.user?.name ?? '';
		email = authStore.user?.email ?? '';
	});

	async function saveProfile() {
		saving = true;
		error = '';
		successMsg = '';
		const res = await fetch('/api/v1/me', {
			method: 'PATCH',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name, email })
		});
		saving = false;
		if (res.ok) {
			const updated = await res.json();
			authStore.set(updated);
			successMsg = 'Profile updated.';
		} else {
			const data = await res.json();
			error = data.message ?? 'Failed to save profile.';
		}
	}

	async function changePassword() {
		if (newPassword !== confirmPassword) {
			error = 'Passwords do not match.';
			return;
		}
		changingPassword = true;
		error = '';
		successMsg = '';
		const res = await fetch('/api/v1/me/password', {
			method: 'PATCH',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ current_password: currentPassword, new_password: newPassword })
		});
		changingPassword = false;
		if (res.ok) {
			successMsg = 'Password changed.';
			currentPassword = '';
			newPassword = '';
			confirmPassword = '';
		} else {
			const data = await res.json();
			error = data.message ?? 'Failed to change password.';
		}
	}

	const inputBase =
		'w-full h-9 bg-white border border-border rounded-md px-3 text-[13px] text-text-main outline-none transition-all duration-150 focus:border-primary focus:shadow-[0_0_0_3px_rgba(59,130,246,.08)] disabled:opacity-60 disabled:cursor-not-allowed';
</script>

<div class="max-w-[560px] py-8 px-6">
	<div class="flex items-center gap-4 mb-8">
		{#if authStore.user}
			<div
				class="w-14 h-14 rounded-full bg-primary text-white text-xl font-semibold flex items-center justify-center shrink-0"
			>
				{authStore.initials}
			</div>
			<div>
				<div class="text-base font-semibold text-text-main">{authStore.user.name}</div>
				<div class="text-[13px] text-text-muted mt-0.5">
					{authStore.user.email} &middot; {authStore.user.role}
				</div>
			</div>
		{/if}
	</div>

	{#if error}
		<div
			class="py-2.5 px-3.5 rounded-md text-[13px] mb-4 bg-red-50 text-red-600 border border-red-200"
		>
			{error}
		</div>
	{/if}
	{#if successMsg}
		<div
			class="py-2.5 px-3.5 rounded-md text-[13px] mb-4 bg-green-50 text-green-600 border border-green-200"
		>
			{successMsg}
		</div>
	{/if}

	<section class="bg-slate-50 border border-border rounded-lg pt-5 px-5 pb-4 mb-4">
		<h2 class="text-[13px] font-semibold text-text-muted uppercase tracking-[0.04em] mb-4">
			Personal info
		</h2>
		<div class="flex flex-col gap-1 mb-3">
			<label class="text-xs font-medium text-text-muted">Name</label>
			<input class={inputBase} bind:value={name} type="text" />
		</div>
		<div class="flex flex-col gap-1 mb-3">
			<label class="text-xs font-medium text-text-muted">Email</label>
			<input
				class={inputBase}
				bind:value={email}
				type="email"
				disabled={authStore.user?.auth_provider !== 'password'}
			/>
		</div>
		<div class="flex justify-end mt-2">
			<button
				onclick={saveProfile}
				disabled={saving}
				class="h-9 px-4 rounded-md text-[13px] font-semibold cursor-pointer transition-colors duration-150 border-none bg-primary text-white hover:enabled:bg-primary-hover disabled:opacity-60 disabled:cursor-not-allowed"
			>
				{saving ? 'Saving...' : 'Save'}
			</button>
		</div>
	</section>

	{#if authStore.user?.auth_provider === 'password'}
		<section class="bg-slate-50 border border-border rounded-lg pt-5 px-5 pb-4 mb-4">
			<h2 class="text-[13px] font-semibold text-text-muted uppercase tracking-[0.04em] mb-4">
				Change password
			</h2>
			<div class="flex flex-col gap-1 mb-3">
				<label class="text-xs font-medium text-text-muted">Current password</label>
				<input class={inputBase} bind:value={currentPassword} type="password" />
			</div>
			<div class="flex flex-col gap-1 mb-3">
				<label class="text-xs font-medium text-text-muted">New password</label>
				<input class={inputBase} bind:value={newPassword} type="password" />
			</div>
			<div class="flex flex-col gap-1 mb-3">
				<label class="text-xs font-medium text-text-muted">Confirm new password</label>
				<input class={inputBase} bind:value={confirmPassword} type="password" />
			</div>
			<div class="flex justify-end mt-2">
				<button
					onclick={changePassword}
					disabled={changingPassword}
					class="h-9 px-4 rounded-md text-[13px] font-semibold cursor-pointer transition-colors duration-150 border-none bg-primary text-white hover:enabled:bg-primary-hover disabled:opacity-60 disabled:cursor-not-allowed"
				>
					{changingPassword ? 'Changing...' : 'Change password'}
				</button>
			</div>
		</section>
	{/if}
</div>
