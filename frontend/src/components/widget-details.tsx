import '../styles/features/widgets-modal.css';
import type { Widget } from '../domain/types';

export function widgetDetail(widget: Widget): string {
  return widget.description || (widget.mount ? `/${widget.mount}` : 'Local widget');
}

export function WidgetLoadErrors({ errors }: { errors: string[] }) {
  if (!errors.length) return null;
  return (
    <p class="widget-load-errors" role="alert">
      {errors.join('\n')}
    </p>
  );
}
