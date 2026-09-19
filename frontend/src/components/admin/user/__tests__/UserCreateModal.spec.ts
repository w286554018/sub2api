import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UserCreateModal from '../UserCreateModal.vue'

const { authState } = vi.hoisted(() => ({
  authState: {
    isSuperAdmin: false,
  },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: vi.fn(),
    showError: vi.fn(),
  }),
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const SelectStub = {
  props: ['options'],
  template: '<div data-test="role-options">{{ options.map(option => option.value).join(",") }}</div>',
}

const mountModal = () => mount(UserCreateModal, {
  props: { show: true },
  global: {
    stubs: {
      BaseDialog: {
        props: ['show'],
        template: '<div v-if="show"><slot /><slot name="footer" /></div>',
      },
      Select: SelectStub,
      Icon: true,
      TotpStepUpDialog: true,
    },
  },
})

describe('UserCreateModal role permissions', () => {
  beforeEach(() => {
    authState.isSuperAdmin = false
  })

  it('ordinary administrators can only create normal users', () => {
    const wrapper = mountModal()

    expect(wrapper.get('[data-test="role-options"]').text()).toBe('user')
  })

  it('super administrators can create every supported role', () => {
    authState.isSuperAdmin = true

    const wrapper = mountModal()

    expect(wrapper.get('[data-test="role-options"]').text()).toBe('user,admin,super_admin')
  })
})
