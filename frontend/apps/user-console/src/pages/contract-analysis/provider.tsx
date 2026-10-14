import {useState,type ComponentType} from 'react';
import {QueryClient,QueryClientProvider} from '@tanstack/react-query';

// Only contract pages use this cache. AuthProvider remounts the route subtree
// when the selected workspace changes; query keys also carry workspace_id.
export function withContractQueries(Page:ComponentType) {
 return function ContractQueryPage(){
  const [client]=useState(()=>new QueryClient({defaultOptions:{queries:{retry:1,staleTime:15000,refetchOnWindowFocus:false}}}));
  return <QueryClientProvider client={client}><Page/></QueryClientProvider>;
 };
}
