// =============================================================================
// DataTable.jsx — one column spec, two renderings
// =============================================================================
// Wide tables are unreadable at 360px in any column arrangement, and
// horizontal scroll hides data behind a gesture. Below the mobile breakpoint
// Lattice shows a card list instead: one card per row, a title line, and .kv
// rows for the rest. Both renderings come from the same column spec here so
// they cannot drift; lattice.css decides which one is visible.
//
// Columns:
//   key         stable identifier, also the cardTitleKey selector
//   label       column header, and the .k label inside a card
//   render      (row) => ReactNode
//   className   static class applied to the <td> and to the card's .v. A
//               class scoped to `.lat-table <selector>` in CSS (as opposed
//               to a bare, unscoped selector) only reaches the <td> — it
//               silently drops on the card rendering, since the card is not
//               inside a .lat-table. Define status/value classes unscoped
//               (see .badge-ok/.badge-warn/.badge-crit/.mono in lattice.css)
//               so they apply to both.
//   headerClass static class applied to the <th> only (e.g. 'num' for
//               right-aligned numeric columns) — not forwarded to <td> or
//               the card, since header-only styling (alignment) usually
//               isn't what a data cell or card value wants.
//   cellClass   (row) => string, for per-row status classes (badge-ok etc.)

import React from 'react';

function cellClassName(col, row) {
  const dynamic = col.cellClass ? col.cellClass(row) : '';
  return [col.className, dynamic].filter(Boolean).join(' ');
}

export default React.memo(function DataTable({
  columns,
  rows,
  rowKey,
  emptyLabel,
  cardTitleKey,
  ariaLabel,
}) {
  if (rows.length === 0) {
    return <div className="lat-empty">{emptyLabel}</div>;
  }

  const titleKey = cardTitleKey ?? columns[0].key;
  const titleCol = columns.find((c) => c.key === titleKey) ?? columns[0];
  const bodyCols = columns.filter((c) => c.key !== titleCol.key);

  return (
    <div className="lat-tabular">
      <div className="table-scroll">
        <table className="lat-table" aria-label={ariaLabel}>
          <thead>
            <tr>
              {columns.map((col) => (
                <th key={col.key} className={col.headerClass}>{col.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={rowKey(row, i)}>
                {columns.map((col) => (
                  <td key={col.key} className={cellClassName(col, row)}>
                    {col.render(row)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ul className="lat-cardlist" aria-label={ariaLabel}>
        {rows.map((row, i) => (
          <li className="lat-card" key={rowKey(row, i)}>
            <div className={`lat-card-head ${cellClassName(titleCol, row)}`.trim()}>
              {titleCol.render(row)}
            </div>
            {bodyCols.map((col) => (
              <div className="kv" key={col.key}>
                <span className="k">{col.label}</span>
                <span className={`v ${cellClassName(col, row)}`.trim()}>
                  {col.render(row)}
                </span>
              </div>
            ))}
          </li>
        ))}
      </ul>
    </div>
  );
});
