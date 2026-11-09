/**
 * Deterministic key for a 1:1 conversation: "min(idA,idB):max(idA,idB)".
 * Stored unique so the database — not application branching — enforces the
 * one-conversation-per-pair invariant under concurrency.
 */
export function directKey(a: string, b: string): string {
  if (!a || !b) {
    throw new Error('directKey requires two user ids');
  }
  if (a === b) {
    throw new Error('directKey requires two distinct users');
  }
  return a < b ? `${a}:${b}` : `${b}:${a}`;
}
