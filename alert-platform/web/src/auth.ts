import { useQuery } from "@tanstack/react-query";
import { api, CurrentUser, setGuestSessionFlag } from "./api";

export function useCurrentUser() {
  return useQuery<CurrentUser>({
    queryKey: ["me"],
    queryFn: async () => {
      const user = await api.get<CurrentUser>("/auth/me");
      setGuestSessionFlag(user.guest);
      return user;
    },
    retry: false,
  });
}
