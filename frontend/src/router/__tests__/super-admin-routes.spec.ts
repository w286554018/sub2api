import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    checkAuth: vi.fn(),
    isAuthenticated: false,
    isAdmin: false,
    isSuperAdmin: false,
    isSimpleMode: false,
    hasPendingAuthSession: false,
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    siteName: 'Sub2API',
    backendModeEnabled: false,
    cachedPublicSettings: null,
  }),
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

describe('super administrator routes', () => {
  it.each(['AdminPlugins', 'AdminSettings'])('%s requires a super administrator', async (name) => {
    const { default: router } = await import('@/router')
    const route = router.getRoutes().find((record) => record.name === name)

    expect(route?.meta.requiresAdmin).toBe(true)
    expect(route?.meta.requiresSuperAdmin).toBe(true)
  })
})
