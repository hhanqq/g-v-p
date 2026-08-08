import { useQuery } from "@tanstack/react-query";
import { api, CurrentUser } from "./api";

export function useCurrentUser() {
  return useQuery<CurrentUser>({
    queryKey: ["me"],
    queryFn: () => api.get<CurrentUser>("/auth/me"),
    retry: false,
  });
}
