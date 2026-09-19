// =============================================================================
// DataTable.test.jsx — dual table/card render used by every wide data panel
// =============================================================================

import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import DataTable from '../../components/DataTable.jsx';

const COLUMNS = [
  { key: 'node', label: 'Node', render: (r) => r.node },
  { key: 'hops', label: 'Hops', render: (r) => r.hops },
  { key: 'snr', label: 'SNR', render: (r) => r.snr, cellClass: (r) => (r.snr < 10 ? 'badge-crit' : 'badge-ok') },
];

const ROWS = [
  { node: 'RAVEN-9ab7', hops: 1, snr: 22 },
  { node: 'Venice-fa2c', hops: 2, snr: 4 },
];

function renderTable(overrides = {}) {
  const props = {
    columns: COLUMNS,
    rows: ROWS,
    rowKey: (r) => r.node,
    emptyLabel: 'No neighbors reporting',
    ariaLabel: 'Mesh peers',
    ...overrides,
  };
  return render(<DataTable {...props} />);
}

afterEach(() => {
  cleanup();
});

describe('TestDataTableDualRender', () => {
  it('renders a table and a card list from the same rows', () => {
    const { container } = renderTable();
    expect(container.querySelectorAll('.lat-table tbody tr')).toHaveLength(2);
    expect(container.querySelectorAll('.lat-cardlist .lat-card')).toHaveLength(2);
  });

  it('renders one card head per row using the first column', () => {
    const { container } = renderTable();
    const heads = [...container.querySelectorAll('.lat-card-head')].map((el) => el.textContent);
    expect(heads).toEqual(['RAVEN-9ab7', 'Venice-fa2c']);
  });

  it('renders the non-title columns as kv rows inside each card', () => {
    const { container } = renderTable();
    const firstCard = container.querySelector('.lat-card');
    const labels = [...firstCard.querySelectorAll('.kv .k')].map((el) => el.textContent);
    const values = [...firstCard.querySelectorAll('.kv .v')].map((el) => el.textContent);
    expect(labels).toEqual(['Hops', 'SNR']);
    expect(values).toEqual(['1', '22']);
  });

  it('applies cellClass to both the table cell and the card value', () => {
    const { container } = renderTable();
    const critCell = container.querySelector('.lat-table tbody tr:nth-child(2) td:nth-child(3)');
    const critValue = container.querySelectorAll('.lat-card')[1].querySelectorAll('.kv .v')[1];
    expect(critCell.className).toContain('badge-crit');
    expect(critValue.className).toContain('badge-crit');
  });

  it('honours an explicit cardTitleKey', () => {
    const { container } = renderTable({ cardTitleKey: 'snr' });
    const heads = [...container.querySelectorAll('.lat-card-head')].map((el) => el.textContent);
    expect(heads).toEqual(['22', '4']);
    const labels = [...container.querySelector('.lat-card').querySelectorAll('.kv .k')].map((el) => el.textContent);
    expect(labels).toEqual(['Node', 'Hops']);
  });

  it('renders the column headers', () => {
    const { container } = renderTable();
    const headers = [...container.querySelectorAll('.lat-table th')].map((el) => el.textContent);
    expect(headers).toEqual(['Node', 'Hops', 'SNR']);
  });
});

describe('TestDataTableEmpty', () => {
  it('renders the empty label and neither table nor cards', () => {
    const { container } = renderTable({ rows: [] });
    expect(screen.getByText('No neighbors reporting')).toBeInTheDocument();
    expect(container.querySelector('.lat-table')).toBeNull();
    expect(container.querySelector('.lat-cardlist')).toBeNull();
  });
});
