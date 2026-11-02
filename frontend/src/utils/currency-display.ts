// Preserve the caller's existing defaults until an administrator selects a precision.
export function currencyFractionDigits(
  decimals: number | null | undefined,
  minimumFractionDigits?: number,
  maximumFractionDigits?: number
): Intl.NumberFormatOptions {
  return {
    minimumFractionDigits: decimals ?? minimumFractionDigits,
    maximumFractionDigits: decimals ?? maximumFractionDigits,
  };
}
