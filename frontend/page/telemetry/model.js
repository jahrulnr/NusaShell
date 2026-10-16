const numberFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

export function formatTelemetryNumber(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return '0';
  return new Intl.NumberFormat(undefined, {
    notation: Math.abs(number) >= 1000 ? 'compact' : 'standard',
    maximumFractionDigits: 1,
  }).format(number);
}

export function formatTelemetryMoney(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return '$0.00';
  const absolute = Math.abs(number);
  return new Intl.NumberFormat(undefined, {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: absolute >= 1 ? 2 : absolute >= 0.01 ? 4 : 6,
  }).format(number);
}

export function formatTelemetryPercent(value) {
  const number = Number(value);
  return `${(Number.isFinite(number) ? number : 0).toFixed(1)}%`;
}

export function formatTelemetryDate(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}

export function telemetryHasUsage(report) {
  if (Number(report?.summary?.total_requests) > 0) return true;
  return Array.isArray(report?.series) && report.series.some((bucket) => Number(bucket?.requests) > 0);
}

export function telemetryReportParts(report) {
  return {
    summary: report?.summary && typeof report.summary === 'object' ? report.summary : {},
    series: Array.isArray(report?.series) ? report.series : [],
    models: Array.isArray(report?.top_models) ? report.top_models : [],
    providers: Array.isArray(report?.top_providers) ? report.top_providers : [],
  };
}

export function formatTokenLabel(value) {
  return numberFormatter.format(Number(value) || 0);
}
