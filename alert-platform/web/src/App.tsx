import { Route, Routes } from "react-router-dom";
import Sidebar from "./components/Sidebar";
import { useCurrentUser } from "./auth";
import Login from "./pages/Login";
import Home from "./pages/Home";
import Incidents from "./pages/Incidents";
import IncidentDetail from "./pages/IncidentDetail";
import Alerts from "./pages/Alerts";
import Equipment from "./pages/Equipment";
import EquipmentDetail from "./pages/EquipmentDetail";
import Employees from "./pages/Employees";
import EmployeeDetail from "./pages/EmployeeDetail";
import Scenarios from "./pages/Scenarios";
import ScenarioEditor from "./pages/ScenarioEditor";
import Sla from "./pages/Sla";
import Analytics from "./pages/Analytics";
import Integrations from "./pages/Integrations";

export default function App() {
  const { data: user, isLoading, isError } = useCurrentUser();

  if (isLoading) {
    return <div className="flex h-screen items-center justify-center text-muted">Загрузка…</div>;
  }
  if (isError || !user) {
    return <Login />;
  }

  return (
    <div className="flex h-screen">
      <Sidebar user={user} />
      <main className="flex-1 overflow-y-auto p-8">
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/incidents" element={<Incidents />} />
          <Route path="/incidents/:id" element={<IncidentDetail />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/equipment" element={<Equipment />} />
          <Route path="/equipment/:id" element={<EquipmentDetail />} />
          <Route path="/employees" element={<Employees />} />
          <Route path="/employees/:id" element={<EmployeeDetail />} />
          <Route path="/scenarios" element={<Scenarios />} />
          <Route path="/scenarios/new" element={<ScenarioEditor />} />
          <Route path="/scenarios/:id/edit" element={<ScenarioEditor />} />
          <Route path="/sla" element={<Sla />} />
          <Route path="/analytics" element={<Analytics />} />
          <Route path="/integrations" element={<Integrations />} />
        </Routes>
      </main>
    </div>
  );
}
