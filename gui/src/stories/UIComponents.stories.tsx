import type { Story } from '@ladle/react';
import { Switch } from '../components/ui/switch';
import { Separator } from '../components/ui/separator';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table';
import { useState } from 'react';

export const SwitchDefault: Story = () => {
  const [checked, setChecked] = useState(false);
  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex items-center gap-3">
        <Switch checked={checked} onCheckedChange={setChecked} />
        <span className="text-sm">{checked ? 'On' : 'Off'}</span>
      </div>
      <div className="flex items-center gap-3">
        <Switch checked={true} disabled />
        <span className="text-sm text-muted-foreground">Disabled (on)</span>
      </div>
      <div className="flex items-center gap-3">
        <Switch checked={false} disabled />
        <span className="text-sm text-muted-foreground">Disabled (off)</span>
      </div>
    </div>
  );
};

export const SeparatorHorizontal: Story = () => (
  <div className="p-4 flex flex-col gap-4">
    <p className="text-sm">Content above</p>
    <Separator />
    <p className="text-sm">Content below</p>
  </div>
);

export const SeparatorVertical: Story = () => (
  <div className="p-4 flex items-center gap-4 h-12">
    <span className="text-sm">Left</span>
    <Separator orientation="vertical" />
    <span className="text-sm">Right</span>
  </div>
);

export const TableExample: Story = () => (
  <div className="p-4">
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Email</TableHead>
          <TableHead>Role</TableHead>
          <TableHead>Created</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow>
          <TableCell className="font-medium">Alice Smith</TableCell>
          <TableCell className="text-muted-foreground">alice@example.com</TableCell>
          <TableCell>Admin</TableCell>
          <TableCell className="text-muted-foreground text-xs">2026-01-01</TableCell>
        </TableRow>
        <TableRow>
          <TableCell className="font-medium">Bob Jones</TableCell>
          <TableCell className="text-muted-foreground">bob@example.com</TableCell>
          <TableCell>User</TableCell>
          <TableCell className="text-muted-foreground text-xs">2026-02-14</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
);
