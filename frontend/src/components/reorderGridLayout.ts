import { translate, type ReorderGridLayout, type LandingGeometry } from './useReorderableList';

/**
 * A grid whose rows wrap into several columns, such as the Hub's agent cards.
 * Landing at position i puts the lifted card in cell i, where the card at i
 * starts; the cards it passes each slide one cell along the reading order,
 * across row ends included, to open it.
 */
function gridGeometry(rects: readonly DOMRect[], from: number): LandingGeometry {
  const cells = rects.map((rect) => ({
    x: rect.left - rects[from].left,
    y: rect.top - rects[from].top,
  }));
  const span = (axis: 'x' | 'y', value: number) =>
    Math.min(
      Math.max(...cells.map((cell) => cell[axis])),
      Math.max(Math.min(...cells.map((cell) => cell[axis])), value),
    );
  return {
    grid: true,
    clamp: (travel) => ({ x: span('x', travel.x), y: span('y', travel.y) }),
    target: (offset) => {
      let to = from;
      let nearest = Infinity;
      cells.forEach((cell, index) => {
        const distance = Math.hypot(cell.x - offset.x, cell.y - offset.y);
        if (distance < nearest) [to, nearest] = [index, distance];
      });
      return to;
    },
    shift: (index, to) => {
      const cell =
        from < index && index <= to ? index - 1 : to <= index && index < from ? index + 1 : index;
      return { x: cells[cell].x - cells[index].x, y: cells[cell].y - cells[index].y };
    },
  };
}

/** Whether rows share one column, measured from their layout boxes. */
const singleColumn = (rects: readonly DOMRect[]): boolean =>
  rects.every((rect) => Math.abs(rect.left - rects[0].left) < 1);

/**
 * Settles dropped grid cards into their new cells. The list has re-rendered
 * in its new order, so each card starts from where it was shown (`shown`) and
 * slides home, exactly like the preview but ending in the real layout.
 */
function settleCards(rows: readonly HTMLElement[], shown: readonly DOMRect[]): void {
  rows.forEach((row, index) => {
    const previous = shown[index];
    if (!previous) return; // The list may have changed before the animation frame.
    const now = row.getBoundingClientRect();
    row.style.transform = translate({
      x: previous.left - now.left,
      y: previous.top - now.top,
    });
  });
  rows[0]?.parentElement?.getBoundingClientRect();
  rows.forEach((row) => {
    row.style.removeProperty('transition');
    row.style.removeProperty('transform');
  });
}

/** Only the Hub's card grid can wrap; the Sidebar lists always use a column. */
export const reorderGridLayout: ReorderGridLayout = {
  singleColumn,
  geometry: gridGeometry,
  settle: settleCards,
};
