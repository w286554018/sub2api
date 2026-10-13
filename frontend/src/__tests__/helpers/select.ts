import { nextTick } from 'vue'
import type { VueWrapper } from '@vue/test-utils'
import Select from '@/components/common/Select.vue'

/**
 * Shared helpers for exercising the real {@link Select} component instead of a native
 * `<select>`: every helper opens the teleported dropdown with a real click and clicks a
 * real option, so tests keep asserting observable user behaviour.
 */

function findSelect(wrapper: VueWrapper, selector: string) {
  const target = wrapper.find(selector)
  const match = wrapper.findAllComponents(Select).find((component) => {
    const el = component.vm.$el as HTMLElement | undefined
    if (!el || typeof el.matches !== 'function') return false
    return target.exists() ? (el === target.element || el.contains(target.element)) : el.matches(selector) || !!el.querySelector(selector)
  })
  if (!match) throw new Error(`Select not found for selector ${selector}`)
  return match
}

/** Option values currently offered by the Select matched by `selector`. */
export function selectOptionValues(wrapper: VueWrapper, selector: string): unknown[] {
  const select = findSelect(wrapper, selector)
  const valueKey = (select.props('valueKey') as string | undefined) ?? 'value'
  const options = select.props('options') as Array<Record<string, unknown> | string | number | boolean | null>
  return options.map((option) => option !== null && typeof option === 'object' ? option[valueKey] : option)
}

/** Open the dropdown of the Select matched by `selector` through its trigger button. */
export async function openSelectDropdown(wrapper: VueWrapper, selector: string): Promise<HTMLElement> {
  const root = wrapper.get(selector)
  const trigger = root.element.tagName === 'BUTTON' ? root : root.find('button')
  // An earlier dropdown may still be open (or leaving its transition). Close it first,
  // then identify the newly mounted portal rather than guessing from DOM order.
  document.body.click()
  await nextTick()
  const oldPortals = new Set(document.body.querySelectorAll<HTMLElement>('.select-dropdown-portal'))
  await trigger.trigger('click')
  await nextTick()
  const portal = Array.from(document.body.querySelectorAll<HTMLElement>('.select-dropdown-portal')).find((el) => !oldPortals.has(el))
  if (portal) return portal
  const scoped = wrapper.findAll('.select-dropdown-portal')
  if (scoped.length) return scoped[scoped.length - 1].element as HTMLElement
  throw new Error(`Select dropdown did not open for selector ${selector}`)
}

/** Click the first option inside an already-open dropdown whose label contains `text`. */
export function clickSelectOptionByText(dropdown: HTMLElement, text: string): void {
  const option = Array.from(dropdown.querySelectorAll<HTMLElement>('.select-option')).find((el) => el.textContent?.includes(text))
  if (!option) throw new Error(`Select option containing "${text}" was not found`)
  option.click()
}

/** Labels of every option inside an already-open dropdown, in render order. */
export function selectDropdownLabels(dropdown: HTMLElement): string[] {
  return Array.from(dropdown.querySelectorAll<HTMLElement>('.select-option')).map((el) => el.textContent?.trim() ?? '')
}

/** Real user flow: open the Select and click the option whose value strictly equals `value`. */
export async function chooseSelectOption(wrapper: VueWrapper, selector: string, value: unknown): Promise<void> {
  const index = selectOptionValues(wrapper, selector).findIndex((candidate) => candidate === value)
  if (index < 0) throw new Error(`Select option value ${String(value)} not found for selector ${selector}`)
  const dropdown = await openSelectDropdown(wrapper, selector)
  const target = dropdown.querySelectorAll<HTMLElement>('.select-option')[index]
  if (!target) throw new Error(`Select option ${index} was not rendered for selector ${selector}`)
  target.click()
  await nextTick()
}

/** Real user flow: open the Select and click the first option whose label contains `text`. */
export async function chooseSelectOptionByText(wrapper: VueWrapper, selector: string, text: string): Promise<void> {
  const dropdown = await openSelectDropdown(wrapper, selector)
  clickSelectOptionByText(dropdown, text)
  await nextTick()
}

/** Current model value bound to the Select matched by `selector`. */
export function selectModelValue(wrapper: VueWrapper, selector: string): unknown {
  return findSelect(wrapper, selector).props('modelValue')
}
