interface PerformanceSample {
  throughput: number | null;
  ttftMs: number | null;
  speedSampleCount: number;
  firstTokenSampleCount: number;
}

export function summarizePerformance(samples: PerformanceSample[]) {
  let speedTotal = 0;
  let speedCount = 0;
  let firstTokenTotal = 0;
  let firstTokenCount = 0;

  for (const sample of samples) {
    if (sample.throughput != null && sample.speedSampleCount > 0) {
      speedTotal += sample.throughput * sample.speedSampleCount;
      speedCount += sample.speedSampleCount;
    }
    if (sample.ttftMs != null && sample.firstTokenSampleCount > 0) {
      firstTokenTotal += sample.ttftMs * sample.firstTokenSampleCount;
      firstTokenCount += sample.firstTokenSampleCount;
    }
  }

  return {
    avgThroughput: speedCount > 0 ? speedTotal / speedCount : null,
    avgTtft: firstTokenCount > 0 ? firstTokenTotal / firstTokenCount : null,
  };
}
