<script lang="ts">
	import { onMount } from 'svelte';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { initLocale, t } from '$lib/i18n';
	import { authStore, type AuthUser } from '$lib/stores/auth.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import PasswordInput from '$lib/ui/PasswordInput.svelte';
	import Select from '$lib/ui/Select.svelte';

	let saving = $state(false);
	let changingPassword = $state(false);
	let error = $state('');
	let successMsg = $state('');

	let name = $state('');
	let email = $state('');
	let locale = $state('');
	const localeOptions = [
		{ value: '', label: t('profile_language_auto') },
		{ value: 'en', label: 'English' },
		{ value: 'pt-BR', label: 'Português (Brasil)' }
	];

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');

	onMount(async () => {
		await authStore.load();
		name = authStore.user?.name ?? '';
		email = authStore.user?.email ?? '';
		locale = authStore.user?.locale ?? '';
		initLocale(locale || null);
	});

	async function saveProfile() {
		saving = true;
		error = '';
		successMsg = '';
		const { data, error: err } = await client.PATCH('/me', {
			body: { name, email, locale: locale === '' ? undefined : (locale as 'en' | 'pt-BR') }
		});
		saving = false;
		if (data) {
			authStore.set(data as AuthUser);
			initLocale(locale || null);
			successMsg = 'Profile updated.';
		} else {
			error = apiErrorMessage(err, 'Failed to save profile.');
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
		const { response, error: err } = await client.PATCH('/me/password', {
			body: { current_password: currentPassword, new_password: newPassword }
		});
		changingPassword = false;
		if (response.ok) {
			successMsg = 'Password changed.';
			currentPassword = '';
			newPassword = '';
			confirmPassword = '';
		} else {
			error = apiErrorMessage(err, 'Failed to change password.');
		}
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-[560px]">
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
			<div class="mb-3">
				<Input label="Name" bind:value={name} />
			</div>
			<div class="mb-3">
				<Input
					label="Email"
					type="email"
					bind:value={email}
					disabled={authStore.user?.auth_provider !== 'password'}
				/>
			</div>
			<div class="mb-3">
				<Select label={t('profile_language')} bind:value={locale} options={localeOptions} />
			</div>
			<div class="flex justify-end mt-2">
				<Button variant="primary" onclick={saveProfile} disabled={saving} loading={saving}>
					{saving ? 'Saving...' : 'Save'}
				</Button>
			</div>
		</section>

		{#if authStore.user?.auth_provider === 'password'}
			<section class="bg-slate-50 border border-border rounded-lg pt-5 px-5 pb-4 mb-4">
				<h2 class="text-[13px] font-semibold text-text-muted uppercase tracking-[0.04em] mb-4">
					Change password
				</h2>
				<div class="mb-3">
					<PasswordInput label="Current password" bind:value={currentPassword} />
				</div>
				<div class="mb-3">
					<PasswordInput
						label="New password"
						autocomplete="new-password"
						bind:value={newPassword}
					/>
				</div>
				<div class="mb-3">
					<PasswordInput
						label="Confirm new password"
						autocomplete="new-password"
						bind:value={confirmPassword}
					/>
				</div>
				<div class="flex justify-end mt-2">
					<Button
						variant="primary"
						onclick={changePassword}
						disabled={changingPassword}
						loading={changingPassword}
					>
						{changingPassword ? 'Changing...' : 'Change password'}
					</Button>
				</div>
			</section>
		{/if}
	</div>
</div>
