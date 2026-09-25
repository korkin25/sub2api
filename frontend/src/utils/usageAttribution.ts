/** Keep the server's decimal cost precision; only pad to a minimum of two places. */
export function attributionCost(value: string): string {
  if (!/^-?\d+(\.\d+)?$/.test(value)) return '—'
  const [whole, fraction = ''] = value.split('.')
  return `$${whole}.${fraction.replace(/0+$/, '').padEnd(2, '0')}`
}
