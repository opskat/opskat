import { JsonTreeView as InternalJsonTreeView } from "../../components/JsonTreeView";

export interface HostJsonTreeViewProps {
  data: unknown;
  className?: string;
}

export function JsonTreeView({ data, className }: HostJsonTreeViewProps) {
  return <InternalJsonTreeView data={data} className={className} />;
}
