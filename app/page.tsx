"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

export default function Home() {
  const text = "CODEHORSES";
  const [displayed, setDisplayed] = useState("");
  const router = useRouter();

  useEffect(() => {
    let i = 0;
    const interval = setInterval(() => {
      setDisplayed(text.slice(0, i + 1));
      i++;
      if (i === text.length) clearInterval(interval);
    }, 120);

    return () => clearInterval(interval);
  }, []);

  return (
    <div className="min-h-screen bg-black text-white flex flex-col items-center justify-center">
      
      {/* Animated Title */}
      <h1 className="text-5xl md:text-7xl font-bold tracking-widest mb-10">
        <span className="bg-gradient-to-r from-purple-500 to-pink-500 bg-clip-text text-transparent">
          {displayed}
        </span>
        <span className="animate-pulse">|</span>
      </h1>

      {/* Button */}
      <button
        onClick={() => router.push("/login")}
        className="px-8 py-4 rounded-xl bg-gradient-to-r from-purple-600 to-pink-600 hover:scale-105 transition text-lg font-semibold shadow-lg"
      >
        Get Started
      </button>

    </div>
  );
}