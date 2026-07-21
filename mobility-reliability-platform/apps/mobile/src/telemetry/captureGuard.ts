/**
 * Synchronous guard for UI operations and native callback generations.
 * React state updates are asynchronous, so they cannot enforce these invariants.
 */
export class CaptureGuard {
  private operationInProgress = false;
  private nextGeneration = 1;
  private activeGeneration: number | null = null;

  tryBeginOperation(): boolean {
    if (this.operationInProgress) return false;
    this.operationInProgress = true;
    return true;
  }

  endOperation(): void {
    this.operationInProgress = false;
  }

  openCapture(): number {
    const generation = this.nextGeneration;
    this.nextGeneration += 1;
    this.activeGeneration = generation;
    return generation;
  }

  closeCapture(): void {
    this.activeGeneration = null;
  }

  acceptsCallback(generation: number): boolean {
    return this.activeGeneration === generation;
  }
}
