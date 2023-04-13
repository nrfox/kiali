export default interface Namespace {
  name: string;
  cluster: string;
  labels?: { [key: string]: string };
}

export const namespacesToString = (namespaces: Namespace[]) => namespaces.map(namespace => namespace.name).join(',');
