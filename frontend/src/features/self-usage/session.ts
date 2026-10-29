const storageKey = 'axonhub.selfUsage.apiKey';

export function getPageKey(): string | null {
  try {
    return sessionStorage.getItem(storageKey);
  } catch {
    return null;
  }
}

export function setPageKey(key: string | null): void {
  try {
    if (key === null) sessionStorage.removeItem(storageKey);
    else sessionStorage.setItem(storageKey, key);
  } catch {
    // Browsers may disable session storage; the active page remains usable.
  }
}

// Initialize the new tab before navigation without putting credentials in the URL or parent session.
export function openSelfUsagePage(key: string): boolean {
  const page = window.open('about:blank', '_blank');
  if (!page) return false;
  try {
    page.sessionStorage.setItem(storageKey, key);
    page.opener = null;
    page.location.replace('/self-usage');
    return true;
  } catch {
    page.close();
    return false;
  }
}
