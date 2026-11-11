import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountTestModal from '../AccountTestModal.vue'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '@/composables/useOpenAIOAuth'

const { getAvailableModels, copyToClipboard } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'admin.accounts.imagePromptDefault': 'Generate a cute orange cat astronaut sticker on a clean pastel background.'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.accounts.imageReceived' && params?.count) {
          return `received-${params.count}`
        }
        if (key === 'admin.accounts.imagePreviewAlt' && params?.index) {
          return `test-image-${params.index}`
        }
        return messages[key] || key
      }
    })
  }
})

function createStreamResponse(lines: string[]) {
  const encoder = new TextEncoder()
  const chunks = lines.map((line) => encoder.encode(line))
  let index = 0

  return {
    ok: true,
    body: {
      getReader: () => ({
        read: vi.fn().mockImplementation(async () => {
          if (index < chunks.length) {
            return { done: false, value: chunks[index++] }
          }
          return { done: true, value: undefined }
        })
      })
    }
  } as Response
}

function mountModal(account: Record<string, unknown> = {
  id: 42,
  name: 'Gemini Image Test',
  platform: 'gemini',
  type: 'apikey',
  status: 'active'
}) {
  return mount(AccountTestModal, {
    props: {
      show: false,
      account
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Select: { template: '<div class="select-stub"></div>' },
        TextArea: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<textarea class="textarea-stub" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        Icon: true
      }
    }
  })
}

async function setGrokMode(wrapper: ReturnType<typeof mountModal>, mode: string) {
  (wrapper.vm as any).grokTestMode = mode
  await flushPromises()
}

async function uploadFile(wrapper: ReturnType<typeof mountModal>, selector: string, file: File) {
  const input = wrapper.get(selector)
  Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
  await input.trigger('change')
  await vi.waitFor(() => {
    expect((wrapper.vm as any).uploadImageDataURL || (wrapper.vm as any).uploadAudioDataURL).toBeTruthy()
  })
}

describe('AccountTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockResolvedValue([
      { id: 'gemini-2.0-flash', display_name: 'Gemini 2.0 Flash' },
      { id: 'gemini-2.5-flash-image', display_name: 'Gemini 2.5 Flash Image' },
      { id: 'gemini-3.1-flash-image', display_name: 'Gemini 3.1 Flash Image' }
    ])
    copyToClipboard.mockReset()
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gemini-2.5-flash-image"}\n',
        'data: {"type":"image","image_url":"data:image/png;base64,QUJD","mime_type":"image/png"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('gemini 图片模型测试会携带提示词并渲染图片预览', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('draw a tiny orange cat astronaut')

    const buttons = wrapper.findAll('button')
    const startButton = buttons.find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gemini-3.1-flash-image',
      prompt: 'draw a tiny orange cat astronaut'
    })

    const preview = wrapper.find('img[alt="test-image-1"]')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('data:image/png;base64,QUJD')
  })

  it('grok 账号测试默认选择 Grok 模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-4.3', display_name: 'Grok 4.3' },
      { id: 'grok-build-0.1', display_name: 'Grok Build 0.1' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"grok-4.3"}\n',
        'data: {"type":"content","text":"ok"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 13,
      name: 'Grok Account',
      platform: 'grok',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const buttons = wrapper.findAll('button')
    const startButton = buttons.find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-4.3',
      prompt: '',
      mode: 'text'
    })
  })

  it('grok 图片模式会上传参考图并发送图片生成请求', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-imagine-image-1', display_name: 'Grok Imagine Image' }
    ])
    const wrapper = mountModal({ id: 14, name: 'Grok Account', platform: 'grok', type: 'oauth', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await setGrokMode(wrapper, 'image')
    ;(wrapper.vm as any).testPrompt = 'draw a red fox'
    await uploadFile(wrapper, 'input[type="file"][accept^="image/"]', new File(['image'], 'reference.png', { type: 'image/png' }))

    await (wrapper.vm as any).startTest()
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-imagine-image-1',
      prompt: 'draw a red fox',
      mode: 'image',
      image_data_url: 'data:image/png;base64,aW1hZ2U='
    })
  })

  it('grok 视频模式会将上传图片作为首帧发送', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-video-1', display_name: 'Grok Video 1' }
    ])
    const wrapper = mountModal({ id: 15, name: 'Grok Account', platform: 'grok', type: 'oauth', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await setGrokMode(wrapper, 'video')
    ;(wrapper.vm as any).testPrompt = 'animate this still'
    await uploadFile(wrapper, 'input[type="file"][accept^="image/"]', new File(['frame'], 'first-frame.png', { type: 'image/png' }))

    await (wrapper.vm as any).startTest()
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-video-1',
      prompt: 'animate this still',
      mode: 'video',
      image_data_url: 'data:image/png;base64,ZnJhbWU='
    })
  })

  it('grok STT 模式会上传音频并调用语音识别请求', async () => {
    const wrapper = mountModal({ id: 16, name: 'Grok Account', platform: 'grok', type: 'oauth', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await setGrokMode(wrapper, 'stt')
    await uploadFile(wrapper, 'input[type="file"][accept^="audio/"]', new File(['audio'], 'sample.wav', { type: 'audio/wav' }))

    await (wrapper.vm as any).startTest()
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: '',
      prompt: '',
      mode: 'stt',
      audio_data_url: 'data:audio/wav;base64,YXVkaW8='
    })
  })

  it('OpenAI Compact 探测会携带 compact 测试模式', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'compact'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({
      model_id: 'gpt-5.4',
      prompt: '',
      mode: 'compact'
    })
  })

  it('BPS 测试失败时展示上游状态码、错误码与消息', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-5.6-sol', display_name: 'GPT-5.6 Sol' }])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gpt-5.6-sol"}\n',
        'data: {"type":"upstream_response","upstream_status":401,"upstream_model":"gpt-5.6-sol","request_id":"req_bps_401","upstream_error_code":"token_invalidated","upstream_error_message":"Bearer [redacted] was rejected"}\n',
        'data: {"type":"error","error":"excel BPS: basispoints_upstream_error (HTTP 401)"}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 44,
      name: 'Excel BPS',
      platform: 'openai',
      type: 'bps-oauth',
      status: 'active',
      credentials: { client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    ;(wrapper.vm as any).selectedModelId = 'gpt-5.6-sol'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    const evidence = wrapper.get('[data-testid="upstream-evidence"]').text()
    expect(evidence).toContain('401')
    expect(evidence).toContain('req_bps_401')
    expect(evidence).toContain('token_invalidated')
    expect(evidence).toContain('Bearer [redacted] was rejected')
  })

  it('只有 BPS OAuth 账号提供 BPS 工具测试，且不提供 compact 测试', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-5.4', display_name: 'GPT-5.4' }])
    const modeValues = async (type: string) => {
      const wrapper = mountModal({ id: 43, name: 'OpenAI account', platform: 'openai', type, status: 'active' })
      await wrapper.setProps({ show: true })
      await flushPromises()
      return ((wrapper.vm as any).openAITestModeOptions as Array<{ value: string }>).map((option) => option.value)
    }

    expect(await modeValues('bps-oauth')).toEqual(['default', 'bps_tools'])
    expect(await modeValues('oauth')).toEqual(['default', 'compact'])
  })
})

describe('AccountTestModal Leonardo', () => {
  beforeEach(() => {
    getAvailableModels.mockReset().mockResolvedValue([{ id: 'gpt-image-2', display_name: 'GPT Image 2' }])
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gpt-image-2"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('中转号用 gpt-image-2 出图测试并携带提示词', async () => {
    const wrapper = mountModal({ id: 51, name: 'Leonardo relay', platform: 'leonardo', type: 'apikey', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('a red fox')

    const startButton = wrapper.findAll('button').find((button) => button.text().includes('admin.accounts.startTest'))
    await startButton!.trigger('click')
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({ model_id: 'gpt-image-2', prompt: 'a red fox' })
  })

  it('Cookie 号同样做出图测试并渲染图片预览', async () => {
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gpt-image-2"}\n',
        'data: {"type":"image","image_url":"data:image/jpeg;base64,QUJD","mime_type":"image/jpeg"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
    const wrapper = mountModal({ id: 52, name: 'Leonardo cookie', platform: 'leonardo', type: 'oauth', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('a red fox')

    const startButton = wrapper.findAll('button').find((button) => button.text().includes('admin.accounts.startTest'))
    await startButton!.trigger('click')
    await flushPromises()
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({ model_id: 'gpt-image-2', prompt: 'a red fox' })
    expect(wrapper.find('img[alt="test-image-1"]').attributes('src')).toBe('data:image/jpeg;base64,QUJD')
  })
})

describe('AccountTestModal Fish', () => {
  beforeEach(() => {
    getAvailableModels.mockReset().mockResolvedValue([{ id: 'gpt-image-2', display_name: 'GPT Image 2' }])
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gpt-image-2"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('中转号按所选模型出图并携带提示词', async () => {
    const wrapper = mountModal({
      id: 61,
      name: 'Fish relay',
      platform: 'fish',
      type: 'apikey',
      status: 'active',
      credentials: { base_url: 'https://relay.example.com', api_key: 'sk-relay' }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('a red fox')

    const startButton = wrapper.findAll('button').find((button) => button.text().includes('admin.accounts.startTest'))
    await startButton!.trigger('click')
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({ model_id: 'gpt-image-2', prompt: 'a red fox' })
  })

  it('token 号按图像模型测试并填入默认提示词', async () => {
    const wrapper = mountModal({
      id: 62,
      name: 'Fish token',
      platform: 'fish',
      type: 'apikey',
      status: 'active',
      credentials: { token: 'tok' }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    expect((promptInput.element as HTMLTextAreaElement).value).toBe(
      'Generate a cute orange cat astronaut sticker on a clean pastel background.'
    )
    const startButton = wrapper.findAll('button').find((button) => button.text().includes('admin.accounts.startTest'))
    await startButton!.trigger('click')
    await flushPromises()

    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gpt-image-2',
      prompt: 'Generate a cute orange cat astronaut sticker on a clean pastel background.'
    })
  })
})


describe('Excel authorization model errors', () => {
  it('shows the authorization state, blocks an empty test and allows reloading', async () => {
    getAvailableModels.mockReset()
    getAvailableModels.mockRejectedValueOnce({reason: 'OPENAI_EXCEL_AUTH_VERIFICATION_REQUIRED', message: 'private error must not be rendered'})
    const wrapper = mountModal()
    await wrapper.setProps({show: true})
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.accounts.excelAuthErrors.OPENAI_EXCEL_AUTH_VERIFICATION_REQUIRED')
    expect(wrapper.text()).not.toContain('private error')
    expect(wrapper.get('[role="alert"] a').attributes('href')).toBe('/admin/token-guard-v2')
    const start = wrapper.findAll('button').find(b => b.text().includes('admin.accounts.startTest'))!
    expect(start.attributes('disabled')).toBeDefined()
    getAvailableModels.mockResolvedValueOnce([{id:'gpt-6-astra',display_name:'Astra'}])
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(start.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})


it('does not show legacy BPS authorization progress for managed authorization connections', async () => {
  getAvailableModels.mockResolvedValue([{id:'gpt-6-sol',display_name:'Sol'}])
  const wrapper = mountModal({id:42,name:'Managed BPS',platform:'openai',type:'oauth',status:'active',extra:{openai_excel_bps:true,openai_excel_bps_authorization_pending:true,openai_connections_managed:true}})
  await wrapper.setProps({show:true})
  await flushPromises()
  expect(wrapper.text()).not.toContain('admin.accounts.bpsAuthorizing')
  wrapper.unmount()
})

it('keeps native model testing available during automatic BPS authorization', async () => {
  getAvailableModels.mockResolvedValue([{id:'gpt-6-sol',display_name:'Sol'}])
  const wrapper = mountModal({id:42,name:'Automatic BPS',platform:'openai',type:'oauth',status:'active',extra:{openai_excel_bps:true,openai_excel_bps_authorization_pending:true}})
  await wrapper.setProps({show:true})
  await flushPromises()
  expect(wrapper.get('[role="status"]').text()).toContain('admin.accounts.bpsAuthorizing')
  const start=wrapper.findAll('button').find(b=>b.text().includes('admin.accounts.startTest'))!
  expect(start.attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})
