// D3 interaction utilities for analytics charts
import * as d3 from 'd3';

export interface TooltipData {
  title: string;
  items: { label: string; value: string | number; color?: string }[];
  x: number;
  y: number;
}

export interface TooltipConfig {
  offset: { x: number; y: number };
  className: string;
  maxWidth: number;
}

export interface ZoomConfig {
  scaleExtent: [number, number];
  translateExtent?: [[number, number], [number, number]];
  onZoom?: (transform: d3.ZoomTransform) => void;
}

export interface BrushConfig {
  extent: [[number, number], [number, number]];
  onBrush?: (selection: [number, number] | null) => void;
  onEnd?: (selection: [number, number] | null) => void;
}

/**
 * Create and manage tooltip for D3 charts
 */
export class ChartTooltip {
  private readonly tooltip: d3.Selection<HTMLDivElement, unknown, null, undefined>;
  private readonly config: TooltipConfig;

  constructor(container: HTMLElement, config: Partial<TooltipConfig> = {}) {
    this.config = {
      offset: { x: 10, y: -10 },
      className: 'chart-tooltip',
      maxWidth: 250,
      ...config,
    };

    // Create tooltip element
    this.tooltip = d3
      .select<HTMLElement, unknown>(container)
      .append('div')
      .attr('class', this.config.className)
      .style('position', 'absolute')
      .style('visibility', 'hidden')
      .style('background-color', 'rgba(0, 0, 0, 0.8)')
      .style('color', 'white')
      .style('padding', '8px 12px')
      .style('border-radius', '4px')
      .style('font-size', '12px')
      .style('font-family', 'system-ui, sans-serif')
      .style('max-width', `${this.config.maxWidth}px`)
      .style('z-index', '1000')
      .style('pointer-events', 'none')
      .style('box-shadow', '0 2px 8px rgba(0, 0, 0, 0.2)');
  }

  show(data: TooltipData): void {
    const content = this.formatTooltipContent(data);

    this.tooltip
      .html(content)
      .style('left', `${data.x + this.config.offset.x}px`)
      .style('top', `${data.y + this.config.offset.y}px`)
      .style('visibility', 'visible')
      .style('opacity', 0)
      .transition()
      .duration(200)
      .style('opacity', 1);
  }

  hide(): void {
    this.tooltip
      .transition()
      .duration(200)
      .style('opacity', 0)
      .on('end', () => {
        this.tooltip.style('visibility', 'hidden');
      });
  }

  move(x: number, y: number): void {
    this.tooltip
      .style('left', `${x + this.config.offset.x}px`)
      .style('top', `${y + this.config.offset.y}px`);
  }

  private formatTooltipContent(data: TooltipData): string {
    let html = `<div style="font-weight: bold; margin-bottom: 4px;">${data.title}</div>`;

    data.items.forEach(item => {
      const colorDot = item.color
        ? `<span style="display: inline-block; width: 8px; height: 8px; border-radius: 50%; background-color: ${item.color}; margin-right: 6px;"></span>`
        : '';
      html += `<div>${colorDot}${item.label}: ${item.value}</div>`;
    });

    return html;
  }

  destroy(): void {
    this.tooltip.remove();
  }
}

/**
 * Add zoom behavior to a chart
 */
export function addZoomBehavior(
  svg: d3.Selection<SVGSVGElement, unknown, null, undefined>,
  config: ZoomConfig
): d3.ZoomBehavior<SVGSVGElement, unknown> {
  const zoom = d3.zoom<SVGSVGElement, unknown>().scaleExtent(config.scaleExtent);

  if (config.translateExtent) {
    zoom.translateExtent(config.translateExtent);
  }

  if (config.onZoom) {
    zoom.on('zoom', event => {
      config.onZoom?.(event.transform);
    });
  }

  svg.call(zoom);

  return zoom;
}

/**
 * Add brush behavior for range selection
 */
export function addBrushBehavior(
  container: d3.Selection<SVGGElement, unknown, null, undefined>,
  config: BrushConfig
): d3.BrushBehavior<unknown> {
  const brush = d3.brushX().extent(config.extent);

  if (config.onBrush) {
    brush.on('brush', event => {
      const selection = event.selection as [number, number] | null;
      config.onBrush?.(selection);
    });
  }

  if (config.onEnd) {
    brush.on('end', event => {
      const selection = event.selection as [number, number] | null;
      config.onEnd?.(selection);
    });
  }

  container.call(brush);

  return brush;
}

/**
 * Add crosshair cursor for multi-line charts
 */
export function addCrosshair(
  chartGroup: d3.Selection<SVGGElement, unknown, null, undefined>,
  config: {
    width: number;
    height: number;
    onMove?: (x: number, y: number) => void;
    onEnter?: () => void;
    onLeave?: () => void;
  }
): void {
  // Create crosshair lines
  const crosshair = chartGroup.append('g').attr('class', 'crosshair').style('display', 'none');

  const verticalLine = crosshair
    .append('line')
    .attr('class', 'crosshair-x')
    .attr('y1', 0)
    .attr('y2', config.height)
    .style('stroke', '#666')
    .style('stroke-width', 1)
    .style('stroke-dasharray', '3,3')
    .style('opacity', 0.7);

  const horizontalLine = crosshair
    .append('line')
    .attr('class', 'crosshair-y')
    .attr('x1', 0)
    .attr('x2', config.width)
    .style('stroke', '#666')
    .style('stroke-width', 1)
    .style('stroke-dasharray', '3,3')
    .style('opacity', 0.7);

  // Add invisible overlay for mouse events
  chartGroup
    .append('rect')
    .attr('class', 'overlay')
    .attr('width', config.width)
    .attr('height', config.height)
    .style('fill', 'none')
    .style('pointer-events', 'all')
    .on('mouseenter', () => {
      crosshair.style('display', null);
      config.onEnter?.();
    })
    .on('mouseleave', () => {
      crosshair.style('display', 'none');
      config.onLeave?.();
    })
    .on('mousemove', function (event) {
      const [x, y] = d3.pointer(event, this);

      verticalLine.attr('x1', x).attr('x2', x);
      horizontalLine.attr('y1', y).attr('y2', y);

      config.onMove?.(x, y);
    });
}

/**
 * Add hover effects to chart elements
 */
export function addHoverEffects<T>(
  elements: d3.Selection<d3.BaseType, T, d3.BaseType, unknown>,
  config: {
    onEnter?: (d: T, element: d3.BaseType) => void;
    onLeave?: (d: T, element: d3.BaseType) => void;
    highlightColor?: string;
    normalOpacity?: number;
    highlightOpacity?: number;
  }
): void {
  const defaultConfig = {
    highlightColor: '#ff6b6b',
    normalOpacity: 0.7,
    highlightOpacity: 1,
    ...config,
  };

  elements
    .on('mouseenter', function (event, d) {
      d3.select(this).style('opacity', defaultConfig.highlightOpacity);

      // Dim other elements
      // eslint-disable-next-line security/detect-object-injection -- Safe: internal array access with controlled index
      elements.filter((_, i, nodes) => nodes[i] !== this).style('opacity', 0.3);

      defaultConfig.onEnter?.(d, this);
    })
    .on('mouseleave', function (event, d) {
      // Restore all elements
      elements.style('opacity', defaultConfig.normalOpacity);

      defaultConfig.onLeave?.(d, this);
    });
}

/**
 * Create legend for multi-series charts
 */
export function createLegend(
  container: d3.Selection<SVGGElement, unknown, null, undefined>,
  config: {
    items: { label: string; color: string; visible: boolean }[];
    position: { x: number; y: number };
    itemHeight: number;
    onToggle?: (label: string, visible: boolean) => void;
  }
): void {
  const legend = container
    .append('g')
    .attr('class', 'legend')
    .attr('transform', `translate(${config.position.x}, ${config.position.y})`);

  const legendItems = legend
    .selectAll('.legend-item')
    .data(config.items)
    .enter()
    .append('g')
    .attr('class', 'legend-item')
    .attr('transform', (_, i) => `translate(0, ${i * config.itemHeight})`)
    .style('cursor', 'pointer')
    .on('click', function (event, d) {
      const newVisible = !d.visible;
      d.visible = newVisible;

      // Update visual state
      d3.select(this)
        .select('rect')
        .style('opacity', newVisible ? 1 : 0.3);

      d3.select(this)
        .select('text')
        .style('opacity', newVisible ? 1 : 0.5);

      config.onToggle?.(d.label, newVisible);
    });

  // Color squares
  legendItems
    .append('rect')
    .attr('x', 0)
    .attr('y', -8)
    .attr('width', 12)
    .attr('height', 12)
    .style('fill', d => d.color)
    .style('opacity', d => (d.visible ? 1 : 0.3));

  // Labels
  legendItems
    .append('text')
    .attr('x', 18)
    .attr('y', 0)
    .attr('dy', '0.32em')
    .style('font-size', '12px')
    .style('font-family', 'system-ui, sans-serif')
    .style('fill', 'currentColor')
    .style('opacity', d => (d.visible ? 1 : 0.5))
    .text(d => d.label);
}
