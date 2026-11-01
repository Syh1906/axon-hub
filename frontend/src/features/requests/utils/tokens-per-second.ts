'use client';

import { useState, useEffect } from 'react';
import type { Request } from '../data/schema';

export type DisplayMode = 'latency' | 'tokensPerSecond';

const VALID_DISPLAY_MODES: DisplayMode[] = ['latency', 'tokensPerSecond'];

/**
 * Calculate the numeric tokens-per-second rate for a given request.
 *
 * @param request - The request object containing usage logs and metrics
 * @returns The numeric rate or null if calculation is not possible
 */
export function getTokensPerSecondValue(request: Request): number | null {
  // The server shares sample selection and timing with the leaderboard.
  const speed = request.outputTokensPerSecond;
  return speed != null && Number.isFinite(speed) && speed > 0 ? speed : null;
}

export function calculateTokensPerSecond(request: Request): string {
  const tokensPerSecond = getTokensPerSecondValue(request);
  return tokensPerSecond == null ? '-' : `${Math.round(tokensPerSecond)} tok/s`;
}

/**
 * Hook to manage display mode state with localStorage persistence.
 * SSR-safe: defaults to 'latency' during server-side rendering.
 * Uses two-phase initialization to avoid hydration mismatches.
 *
 * @returns Tuple of [displayMode, setDisplayMode] similar to useState
 */
export function useDisplayMode(): [DisplayMode, React.Dispatch<React.SetStateAction<DisplayMode>>] {
  const [displayMode, setDisplayMode] = useState<DisplayMode>(() => {
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem('requests-table-latency-display-mode');
      if (stored && VALID_DISPLAY_MODES.includes(stored as DisplayMode)) {
        return stored as DisplayMode;
      }
    }
    return 'latency';
  });

  useEffect(() => {
    if (typeof window !== 'undefined') {
      localStorage.setItem('requests-table-latency-display-mode', displayMode);
    }
  }, [displayMode]);

  return [displayMode, setDisplayMode];
}
