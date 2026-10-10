"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export default function LogoutButton() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);

  const handleLogout = async () => {
    setLoading(true);

    try {
      // Get session token from localStorage
      const sessionToken = localStorage.getItem("session_token");

      // Call the Go API logout endpoint
      if (sessionToken) {
        await fetch("http://localhost:8081/v1/auth/logout", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${sessionToken}`,
          },
        });
      }

      // Clear localStorage
      localStorage.removeItem("session_token");

      // Redirect to login
      router.push("/login");
    } catch (error) {
      console.error("Logout error:", error);
      // Still clear localStorage and redirect even if API call fails
      localStorage.removeItem("session_token");
      router.push("/login");
    } finally {
      setLoading(false);
    }
  };

  return (
    <button
      onClick={handleLogout}
      disabled={loading}
      className="px-4 py-2 bg-red-600 hover:bg-red-700 transition rounded-xl text-sm font-semibold disabled:opacity-50 disabled:cursor-not-allowed"
    >
      {loading ? "Logging out..." : "Logout"}
    </button>
  );
}
