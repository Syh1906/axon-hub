import { test, expect } from '@playwright/test';

test.use({ locale: 'zh-CN', storageState: { cookies: [], origins: [] } });

test('翻译后的请求耗时更新和清空都不触发整页错误', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/tests/language/index.html');
  const duration = page.getByTestId('duration');
  await expect(duration).toContainText('总耗时 —');
  await page.getByRole('button', { name: '模拟翻译', exact: true }).click();
  await page.getByRole('button', { name: '完成请求', exact: true }).click();
  await expect(duration).toContainText('首字 700ms · 总耗时 2.5s');
  await expect(duration).toContainText('42 tok/s');
  await page.getByRole('button', { name: '模拟翻译', exact: true }).click();
  await page.getByRole('button', { name: '重置请求', exact: true }).click();
  await expect(duration).toContainText('总耗时 —');
  await expect(duration).not.toContainText('700ms');
  expect(errors).toEqual([]);
});

test('旧语言缓存不覆盖浏览器，手选保留并可恢复自动', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('i18nextLng', 'en'));
  await page.goto('/tests/language/index.html');
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await page.getByRole('button', { name: '切换语言' }).click();
  await expect(page.getByRole('menuitemradio', { name: '跟随浏览器' })).toHaveAttribute('aria-checked', 'true');
  await page.getByRole('menuitemradio', { name: 'English', exact: true }).click();
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.getByRole('button', { name: 'Toggle language' }).click();
  await page.getByRole('menuitemradio', { name: 'Follow browser', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
});

test('自助页返回时复用账号缓存也先恢复账号语言', async ({ page }) => {
  let meRequests = 0;
  page.on('request', (request) => { if (request.url().endsWith('/admin/graphql')) meRequests++; });
  await page.goto('/tests/language/index.html?cached');
  await expect(page.getByTestId('cached-management')).toHaveAttribute('data-first-language', 'zh');
  await page.getByRole('button', { name: '进入自助页', exact: true }).click();
  await page.getByRole('button', { name: '切换语言', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'English', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.getByRole('button', { name: '返回管理页（复用缓存）', exact: true }).click();
  await expect(page.getByTestId('cached-management')).toHaveAttribute('data-first-language', 'zh');
  await expect(page.getByTestId('cached-management')).toHaveText('跟随浏览器');
  expect(meRequests).toBe(0);
});
