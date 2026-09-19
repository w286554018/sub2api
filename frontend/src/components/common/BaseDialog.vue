<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="show"
        class="modal-overlay"
        :style="zIndexStyle"
        :aria-labelledby="dialogId"
        role="dialog"
        aria-modal="true"
        @click.self="handleClose"
      >
        <!-- Modal panel -->
        <div ref="dialogRef" :class="['modal-content', widthClasses]" tabindex="-1" @click.stop>
          <!-- Header -->
          <div class="modal-header">
            <h3 :id="dialogId" class="modal-title">
              {{ title }}
            </h3>
            <button
              v-if="showCloseButton"
              type="button"
              @click="emit('close')"
              class="-mr-2 rounded-xl p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/30 focus-visible:ring-offset-2 dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300 dark:focus-visible:ring-offset-dark-900"
              aria-label="Close modal"
            >
              <Icon name="x" size="md" />
            </button>
          </div>

          <!-- Body -->
          <div ref="modalBodyRef" class="modal-body">
            <slot></slot>
          </div>

          <!-- Footer -->
          <div v-if="$slots.footer" class="modal-footer">
            <slot name="footer"></slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted, ref, nextTick } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { dialogLayer, isTopDialog, nextDialogId, registerDialog, unregisterDialog } from './dialogStack'

// 生成唯一ID以避免多个对话框时ID冲突
const dialogId = nextDialogId()

// 焦点管理
const dialogRef = ref<HTMLElement | null>(null)
const modalBodyRef = ref<HTMLElement | null>(null)

type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

interface Props {
  show: boolean
  title: string
  width?: DialogWidth
  closeOnEscape?: boolean
  closeOnClickOutside?: boolean
  showCloseButton?: boolean
  zIndex?: number
}

interface Emits {
  (e: 'close'): void
}

const props = withDefaults(defineProps<Props>(), {
  width: 'normal',
  closeOnEscape: true,
  closeOnClickOutside: false,
  showCloseButton: true,
  zIndex: 50
})

const emit = defineEmits<Emits>()

// Custom z-index style (overrides the default z-50 from CSS)
const zIndexStyle = computed(() => {
  return { zIndex: dialogLayer(dialogId, props.zIndex) }
})

const widthClasses = computed(() => {
  // Width guidance: narrow=confirm/short prompts, normal=standard forms,
  // wide=multi-section forms or rich content, extra-wide=analytics/tables,
  // full=full-screen or very dense layouts.
  const widths: Record<DialogWidth, string> = {
    narrow: 'max-w-md',
    normal: 'max-w-lg',
    wide: 'w-full sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
    'extra-wide': 'w-full sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
    full: 'w-full sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl'
  }
  return widths[props.width]
})

const handleClose = () => {
  if (props.closeOnClickOutside && isTopDialog(dialogId)) {
    emit('close')
  }
}

const focusableSelector = [
  'button:not(:disabled)',
  '[href]',
  'input:not([type="hidden"]):not(:disabled)',
  'select:not(:disabled)',
  'textarea:not(:disabled)',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])'
].join(', ')

const isHiddenFromFocus = (element: HTMLElement) => {
  for (let current: HTMLElement | null = element; current; current = current.parentElement) {
    if (current.hidden || current.hasAttribute('inert') || current.getAttribute('aria-hidden') === 'true') {
      return true
    }
    const style = window.getComputedStyle(current)
    if (style.display === 'none' || style.visibility === 'hidden') {
      return true
    }
    if (current === dialogRef.value) break
  }
  return false
}

const getFocusableElements = () => {
  if (!dialogRef.value) return []
  return Array.from(dialogRef.value.querySelectorAll<HTMLElement>(focusableSelector))
    .filter(element => element.tabIndex >= 0 && !isHiddenFromFocus(element))
}

const focusInitialElement = () => {
  const firstFocusable = getFocusableElements()[0]
  if (firstFocusable) {
    firstFocusable.focus()
    return
  }
  dialogRef.value?.focus()
}

const handleTab = (event: KeyboardEvent) => {
  if (!dialogRef.value) return

  const focusableElements = getFocusableElements()
  const firstFocusable = focusableElements[0]
  const lastFocusable = focusableElements.at(-1)

  if (!firstFocusable || !lastFocusable) {
    event.preventDefault()
    dialogRef.value.focus()
    return
  }

  const activeElement = document.activeElement
  if (event.shiftKey && (!dialogRef.value.contains(activeElement) || activeElement === firstFocusable)) {
    event.preventDefault()
    lastFocusable.focus()
  } else if (!event.shiftKey && (!dialogRef.value.contains(activeElement) || activeElement === lastFocusable)) {
    event.preventDefault()
    firstFocusable.focus()
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (!props.show || !isTopDialog(dialogId)) return

  if (props.closeOnEscape && event.key === 'Escape') {
    event.preventDefault()
    event.stopImmediatePropagation()
    emit('close')
  } else if (event.key === 'Tab') {
    handleTab(event)
  }
}

const closeDialog = () => {
  unregisterDialog(dialogId)?.focus()
}

// Prevent body scroll when modal is open and manage focus
watch(
  () => props.show,
  async (isOpen) => {
    if (isOpen) {
      // 保存当前焦点元素
      const previousActiveElement = document.activeElement instanceof HTMLElement ? document.activeElement : null
      // 使用CSS类而不是直接操作style,更易于管理多个对话框
      registerDialog(dialogId, props.zIndex, previousActiveElement)

      // 等待DOM更新后设置焦点到对话框
      await nextTick()
      if (modalBodyRef.value) {
        modalBodyRef.value.scrollTop = 0
      }
      if (props.show && isTopDialog(dialogId)) {
        focusInitialElement()
      }
    } else {
      closeDialog()
    }
  },
  { immediate: true }
)

watch(
  () => props.zIndex,
  zIndex => {
    if (props.show) registerDialog(dialogId, zIndex)
  }
)

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleEscape)
  // 确保组件卸载时移除滚动锁定
  closeDialog()
})
</script>
