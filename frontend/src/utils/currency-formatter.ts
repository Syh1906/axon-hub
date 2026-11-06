// 同一张表的金额格式通常相同，复用浏览器格式器，避免每个单元格重复构造。
const formatters = new Map<string, Intl.NumberFormat>();

export function formatCurrencyValue(value: number, locale: string | undefined, options: Intl.NumberFormatOptions): string {
  const key = JSON.stringify([locale, options]);
  let formatter = formatters.get(key);
  if (!formatter) {
    formatter = new Intl.NumberFormat(locale, options);
    if (formatters.size >= 64) formatters.delete(formatters.keys().next().value!);
    formatters.set(key, formatter);
  }
  return formatter.format(value);
}
