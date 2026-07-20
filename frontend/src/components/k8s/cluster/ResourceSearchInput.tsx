import { Input } from "@opskat/ui";
import { Search } from "lucide-react";

interface ResourceSearchInputProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
}

export function ResourceSearchInput({ value, onChange, placeholder }: ResourceSearchInputProps) {
  return (
    <div className="relative my-1 ml-9 mr-2">
      <Search className="absolute left-2 top-1/2 h-3 w-3 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="h-6 w-full pl-7 text-xs"
      />
    </div>
  );
}
