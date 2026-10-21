<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { localeStore } from '$lib/i18n/locale.svelte';
	import { authStore, type AuthUser } from '$lib/stores/auth.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import PasswordInput from '$lib/ui/PasswordInput.svelte';
	import Select from '$lib/ui/Select.svelte';

	const languageOptions = [
		{ value: 'en', label: m.settings_profile_english() },
		{ value: 'pt-BR', label: m.settings_profile_portugues_brasil() }
	];

	let saving = $state(false);
	let changingPassword = $state(false);
	let error = $state('');
	let successMsg = $state('');

	// The settings layout renders this page once the user is loaded.
	let name = $state(authStore.user?.name ?? '');
	let email = $state(authStore.user?.email ?? '');
	let locale = $state(authStore.user?.locale ?? localeStore.current);

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');

	async function saveProfile() {
		saving = true;
		error = '';
		successMsg = '';
		const { data, error: err } = await client.PATCH('/me', {
			body: { name, email, locale: locale as 'en' | 'pt-BR' }
		});
		saving = false;
		if (data) {
			authStore.set(data as AuthUser);
			successMsg = m.settings_profile_profile_updated();
		} else {
			error = apiErrorMessage(err, m.settings_profile_failed_to_save_profile());
		}
	}

	async function changePassword() {
		if (newPassword !== confirmPassword) {
			error = m.settings_profile_passwords_do_not_match();
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
			successMsg = m.settings_profile_password_changed();
			currentPassword = '';
			newPassword = '';
			confirmPassword = '';
		} else {
			error = apiErrorMessage(err, m.settings_profile_failed_to_change_password());
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
				{m.settings_profile_personal_info()}
			</h2>
			<div class="mb-3">
				<Input label={m.common_name()} bind:value={name} />
			</div>
			<div class="mb-3">
				<Input
					label={m.common_email()}
					type="email"
					bind:value={email}
					disabled={authStore.user?.auth_provider !== 'local'}
				/>
			</div>
			<div class="flex justify-end mt-2">
				<Button variant="primary" onclick={saveProfile} disabled={saving} loading={saving}>
					{saving ? m.settings_profile_saving() : 'Save'}
				</Button>
			</div>
		</section>

		<section class="bg-slate-50 border border-border rounded-lg pt-5 px-5 pb-4 mb-4">
			<h2 class="text-[13px] font-semibold text-text-muted uppercase tracking-[0.04em] mb-4">
				{m.settings_profile_language()}
			</h2>
			<div class="mb-3">
				<Select
					label={m.settings_profile_interface_language()}
					bind:value={locale}
					options={languageOptions}
				/>
			</div>
			<p class="text-[11px] text-text-muted">
				{m.settings_profile_the_change_applies_after_saving_and_follow()}
			</p>
		</section>

		{#if authStore.user?.auth_provider === 'local'}
			<section class="bg-slate-50 border border-border rounded-lg pt-5 px-5 pb-4 mb-4">
				<h2 class="text-[13px] font-semibold text-text-muted uppercase tracking-[0.04em] mb-4">
					{m.settings_profile_change_password()}
				</h2>
				<div class="mb-3">
					<PasswordInput
						label={m.settings_profile_current_password()}
						bind:value={currentPassword}
					/>
				</div>
				<div class="mb-3">
					<PasswordInput
						label={m.settings_profile_new_password()}
						autocomplete="new-password"
						bind:value={newPassword}
					/>
				</div>
				<div class="mb-3">
					<PasswordInput
						label={m.settings_profile_confirm_new_password()}
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
						{changingPassword
							? m.settings_profile_changing()
							: m.settings_profile_change_password()}
					</Button>
				</div>
			</section>
		{/if}
	</div>
</div>
