import { expect, test } from '@playwright/test'

/**
 * GUI smoke suite over the mock preview (npm run dev:mock, see
 * playwright.config.ts): the in-repo fake runtime answers every backend
 * binding, so these specs drive the real app black-box and assert only
 * user-visible elements (roles / text), never implementation details.
 *
 * Mock persona: the fake backend boots an Android persona by default and a
 * Windows desktop persona with the ?sc=desktop query (read once at module
 * load in src/dev/mockData.ts). The hash router path must therefore always
 * ride behind the query string (/?sc=desktop#/path) for desktop-tier tests.
 *
 * Mock UI language: the fake config resolves zh, so the default surface copy
 * asserted below is the zh dictionary text (lib/i18n.ts).
 */

/** Build a mock-preview URL that keeps the desktop persona query in place. */
function mockUrl(path: string): string {
  return `/?sc=desktop#${path}`
}

test.describe('desktop tier', () => {
  test.use({ viewport: { width: 1280, height: 800 } })

  test('sidebar navigates through the five primary destinations', async ({ page }) => {
    // Guards: the desktop nav rail reaches every primary destination and each
    // page renders its signature content.
    await page.goto(mockUrl('/'))
    // Home smart-lands on the System Info tab (runtime installed in the mock):
    // the CPU hardware card is the page's landmark.
    await expect(page.getByText('处理器')).toBeVisible()

    // Chat: model chip + composer input with the desktop placeholder copy.
    await page.getByRole('link', { name: '聊天' }).click()
    await expect(page.getByRole('button', { name: '模型' })).toBeVisible()
    await expect(page.getByPlaceholder('输入消息，Enter 发送，Shift+Enter 换行')).toBeVisible()

    // Models: the download tab is the default landing tab and owns the search box.
    await page.getByRole('link', { name: '模型' }).click()
    await expect(page.getByPlaceholder('搜索模型，如 Qwen3、LLaMA…')).toBeVisible()

    // API: service hero shows the running status (mock server boots running).
    await page.getByRole('link', { name: 'API' }).click()
    await expect(page.getByRole('heading', { name: 'API 路由' })).toBeVisible()
    await expect(page.getByText('运行中')).toBeVisible()

    // Settings: appearance group with the dark-mode switch.
    await page.getByRole('link', { name: '设置' }).click()
    await expect(page.getByRole('heading', { name: '设置' })).toBeVisible()
    await expect(page.getByRole('switch', { name: '深色模式' })).toBeVisible()
  })

  test('/downloads redirects to the models download tab', async ({ page }) => {
    // Guards: the legacy /downloads path keeps working after the Models shell merge.
    await page.goto(mockUrl('/downloads'))
    await expect(page).toHaveURL(/#\/models\/download$/)
    await expect(page.getByPlaceholder('搜索模型，如 Qwen3、LLaMA…')).toBeVisible()
  })

  test('switching to dark theme applies html[data-theme] and survives reload', async ({ page }) => {
    // Guards: the theme switch flips the document attribute and the preference
    // is persisted (localStorage first-frame snapshot + backend config).
    await page.goto(mockUrl('/settings'))
    const themeSwitch = page.getByRole('switch', { name: '深色模式' })
    await expect(themeSwitch).toHaveAttribute('aria-checked', 'false')
    await themeSwitch.click()
    await expect(themeSwitch).toHaveAttribute('aria-checked', 'true')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    const stored = await page.evaluate(() => localStorage.getItem('llama-desktop-theme'))
    expect(stored).toBe('dark')
    // A reload must come back dark (snapshot + mock GetConfig both persist it).
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    await expect(page.getByRole('switch', { name: '深色模式' })).toHaveAttribute('aria-checked', 'true')
  })

  test('switching language zh to en re-renders stable copy in English', async ({ page }) => {
    // Guards: the language picker re-localizes the whole surface immediately
    // (mock SetLanguage resolves to en, setLocale re-renders the dictionary).
    await page.goto(mockUrl('/settings'))
    await expect(page.getByRole('heading', { name: '设置' })).toBeVisible()
    await page.getByRole('button', { name: '界面语言' }).click()
    await page.getByRole('option', { name: 'English' }).click()
    // The page title and an existing control label both flip to their en copy.
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
    await expect(page.getByRole('heading', { name: '设置' })).toHaveCount(0)
    await expect(page.getByRole('switch', { name: 'Dark Mode' })).toBeVisible()
  })

  test('sending a chat message streams the mock assistant reply and the vision attach stays enabled', async ({ page }) => {
    // Guards: the full chat loop works against the mock — model pick, composer
    // send, SSE streaming into an assistant bubble — and picking the 👁️
    // vision-capable model keeps the image-attach entry usable (issue #35 gate).
    await page.goto(mockUrl('/chat'))
    // Pick the vision-capable model from the chip (mock marks it hasMmproj);
    // exact: true keeps the chip locator away from the TaskDock "任务与模型" pill.
    await page.getByRole('button', { name: '模型', exact: true }).click()
    await page.getByRole('option', { name: /Qwen3-4B/ }).click()
    await expect(page.getByRole('button', { name: '添加图片' })).toBeEnabled()
    // Send a message; the mock service is already running so streaming starts
    // straight away (auto-start covers the stopped case in real usage).
    const input = page.getByPlaceholder('输入消息，Enter 发送，Shift+Enter 换行')
    await input.fill('Hello')
    await input.press('Enter')
    // The canned en reply streams at a visible pace; allow 15s for the SSE run.
    await expect(page.getByText('llama-server instance on this machine')).toBeVisible({ timeout: 15_000 })
  })
})

test.describe('mobile tier', () => {
  // Phone frame reference viewport (docs design draft 390×844): the bottom tab
  // bar replaces the sidebar at <=767px (lib/layout.ts MOBILE_MAX).
  test.use({ viewport: { width: 390, height: 844 } })

  test('bottom tab bar renders and the chat composer uses the short placeholder', async ({ page }) => {
    // Guards: the phone shell shows the MobileNav destinations and the touch
    // composer copy (short placeholder, no keyboard hints).
    await page.goto('/#/')
    for (const label of ['本机', '聊天', '模型', 'API', '设置']) {
      await expect(page.getByRole('link', { name: label })).toBeVisible()
    }
    await page.getByRole('link', { name: '聊天' }).click()
    await expect(page.getByPlaceholder('输入消息')).toHaveAttribute('placeholder', '输入消息')
  })
})
