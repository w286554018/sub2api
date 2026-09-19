import { shallowReactive } from 'vue'

interface DialogEntry {
  id: string
  zIndex: number
  restoreTargets: HTMLElement[]
}

const dialogs = shallowReactive<DialogEntry[]>([])
let sequence = 0

export const nextDialogId = () => `modal-title-${++sequence}`

export function registerDialog(id: string, zIndex: number, restoreTarget?: HTMLElement | null) {
  const index = dialogs.findIndex(dialog => dialog.id === id)
  if (index >= 0) {
    if (dialogs[index].zIndex !== zIndex) {
      dialogs[index] = { ...dialogs[index], zIndex }
    }
  } else {
    dialogs.push({ id, zIndex, restoreTargets: restoreTarget ? [restoreTarget] : [] })
  }
  document.body.classList.add('modal-open')
}

export function unregisterDialog(id: string) {
  const index = dialogs.findIndex(dialog => dialog.id === id)
  if (index < 0) {
    document.body.classList.toggle('modal-open', dialogs.length > 0)
    return null
  }

  const wasTopDialog = index === dialogs.length - 1
  const [removed] = dialogs.splice(index, 1)
  if (!wasTopDialog && dialogs[index]) {
    const successor = dialogs[index]
    dialogs[index] = {
      ...successor,
      restoreTargets: [...removed.restoreTargets, ...successor.restoreTargets]
    }
  }
  document.body.classList.toggle('modal-open', dialogs.length > 0)
  if (!wasTopDialog) return null
  return removed.restoreTargets.find(target => target.isConnected) ?? null
}

export const isTopDialog = (id: string) => dialogs.at(-1)?.id === id

export function dialogLayer(id: string, fallback: number) {
  let layer = 40
  for (const dialog of dialogs) {
    layer = Math.max(layer + 10, dialog.zIndex)
    if (dialog.id === id) {
      return layer
    }
  }
  return fallback
}
