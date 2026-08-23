import { useEffect, useRef } from "react";
import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { v4 as uuidv4 } from "uuid";
import "./App.css";

type PlayerPosition = { xPos: number; yPos: number };

type ServerMessage =
  | { type: "state"; players: Record<string, PlayerPosition> }
  | { type: "join"; userId: string; position: PlayerPosition }
  | { type: "leave"; userId: string };

const FIELD_SIZE = 500;
const POSITION_SCALE = 5;

function colorForPlayer(userId: string): string {
  let hash = 0;
  for (let i = 0; i < userId.length; i++) {
    hash = (hash * 31 + userId.charCodeAt(i)) | 0;
  }
  return `hsl(${Math.abs(hash) % 360}, 70%, 55%)`;
}

function App() {
  const queryClient = useQueryClient();
  const socketRef = useRef<WebSocket | null>(null);

  const { data: players } = useQuery<Record<string, PlayerPosition>>({
    queryKey: ["players"],
    queryFn: skipToken,
    initialData: {},
  });
  const { data: activity } = useQuery<string[]>({
    queryKey: ["activity"],
    queryFn: skipToken,
    initialData: [],
  });

  useEffect(() => {
    const userId = uuidv4();
    const xPos = Math.floor(Math.random() * 100);
    const yPos = Math.floor(Math.random() * 100);
    const socket = new WebSocket(
      `ws://localhost:8080/echo?userId=${userId}&xPos=${xPos}&yPos=${yPos}`,
    );
    socketRef.current = socket;

    socket.onopen = () => {
      socket.send("hello from frontend");
    };

    socket.onmessage = (event) => {
      const message: ServerMessage = JSON.parse(event.data);

      if (message.type === "state") {
        queryClient.setQueryData(["players"], message.players);
        return;
      }

      if (message.type === "join") {
        queryClient.setQueryData(
          ["players"],
          (current: Record<string, PlayerPosition> = {}) => ({
            ...current,
            [message.userId]: message.position,
          }),
        );
        queryClient.setQueryData(["activity"], (log: string[] = []) => [
          `${message.userId} joined`,
          ...log,
        ]);
        return;
      }

      if (message.type === "leave") {
        queryClient.setQueryData(
          ["players"],
          (current: Record<string, PlayerPosition> = {}) => {
            const next = { ...current };
            delete next[message.userId];
            return next;
          },
        );
        queryClient.setQueryData(["activity"], (log: string[] = []) => [
          `${message.userId} left`,
          ...log,
        ]);
      }
    };

    return () => {
      socket.close();
      socketRef.current = null;
    };
  }, [queryClient]);

  return (
    <section id="game">
      <h1>Territory</h1>
      <p>Players online: {Object.keys(players ?? {}).length}</p>

      <div className="field" style={{ width: FIELD_SIZE, height: FIELD_SIZE }}>
        {Object.entries(players ?? {}).map(([userId, pos]) => (
          <div
            key={userId}
            className="player-shape"
            title={userId}
            style={{
              left: pos.xPos * POSITION_SCALE,
              top: pos.yPos * POSITION_SCALE,
              backgroundColor: colorForPlayer(userId),
            }}
          />
        ))}
      </div>

      <ul className="activity-log">
        {(activity ?? []).slice(0, 5).map((entry, i) => (
          <li key={i}>{entry}</li>
        ))}
      </ul>
    </section>
  );
}

export default App;
