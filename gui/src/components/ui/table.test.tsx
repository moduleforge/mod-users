import { describe, expect, test } from 'bun:test';
import { render, screen } from '@testing-library/react';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from './table';

function renderSampleTable(bodyClassName?: string) {
  return render(
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Email</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody className={bodyClassName}>
        <TableRow>
          <TableCell>Jane Doe</TableCell>
          <TableCell>jane@example.com</TableCell>
        </TableRow>
      </TableBody>
    </Table>,
  );
}

describe('Table', () => {
  test('renders an accessible table with header and body rows', () => {
    renderSampleTable();

    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader')).toHaveLength(2);
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Email' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Jane Doe' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'jane@example.com' })).toBeInTheDocument();
  });

  test('tags each part with its data-slot attribute', () => {
    renderSampleTable();

    expect(document.querySelector('[data-slot="table"]')).toBeInTheDocument();
    expect(document.querySelector('[data-slot="table-header"]')).toBeInTheDocument();
    expect(document.querySelector('[data-slot="table-body"]')).toBeInTheDocument();
    expect(document.querySelectorAll('[data-slot="table-row"]')).toHaveLength(2);
    expect(document.querySelectorAll('[data-slot="table-head"]')).toHaveLength(2);
    expect(document.querySelectorAll('[data-slot="table-cell"]')).toHaveLength(2);
  });

  test('merges a caller-supplied className with the base classes via cn()', () => {
    renderSampleTable('extra-body-class');

    const body = document.querySelector('[data-slot="table-body"]');
    expect(body).toHaveClass('extra-body-class');
    // The base class defining "no border on last row" should still be present.
    expect(body?.className).toContain('[&_tr:last-child]:border-0');
  });

  test('wraps the table in an overflow container for narrow viewports', () => {
    renderSampleTable();

    const container = document.querySelector('[data-slot="table-container"]');
    expect(container).toBeInTheDocument();
    expect(container).toContainElement(screen.getByRole('table'));
  });
});
