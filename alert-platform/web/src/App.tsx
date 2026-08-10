import { Route, Routes, useLocation } from "react-router-dom";
import Sidebar from "./components/Sidebar";
import { useCurrentUser } from "./auth";
import Login from "./pages/Login";
import Compliance from "./pages/Compliance";
import Home from "./pages/Home";
import Incidents from "./pages/Incidents";
import IncidentDetail from "./pages/IncidentDetail";
import Alerts from "./pages/Alerts";
import Equipment from "./pages/Equipment";
import EquipmentDetail from "./pages/EquipmentDetail";
import Employees from "./pages/Employees";
import AvailabilityCalendar from "./pages/AvailabilityCalendar";
import Coverage from "./pages/Coverage";
import EmployeeDetail from "./pages/EmployeeDetail";
import Groups from "./pages/Groups";
import Scenarios from "./pages/Scenarios";
import ScenarioEditor from "./pages/ScenarioEditor";
import ScenarioStats from "./pages/ScenarioStats";
import Sla from "./pages/Sla";
import Analytics from "./pages/Analytics";
import Integrations from "./pages/Integrations";
import Sources from "./pages/Sources";
import Audit from "./pages/Audit";
import ChangeHistory from "./pages/ChangeHistory";
import Demo from "./pages/Demo";

export default function App() {
  const location = useLocation();
  const { data: user, isLoading, isError } = useCurrentUser();

  // Страница «Соответствие критериям» намеренно публична — рассчитана на
  // судью/проверяющего без LDAP-логина, как и её предшественница на
  // Python (/console/compliance). Единственный маршрут вне LDAP-сессии.
  if (location.pathname === "/compliance") {
    return <Compliance />;
  }

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
          <Route path="/availability" element={<AvailabilityCalendar />} />
          <Route path="/coverage" element={<Coverage />} />
          <Route path="/groups" element={<Groups />} />
          <Route path="/scenarios" element={<Scenarios />} />
          <Route path="/scenarios/new" element={<ScenarioEditor />} />
          <Route path="/scenarios/:id/edit" element={<ScenarioEditor />} />
          <Route path="/scenarios/:id/stats" element={<ScenarioStats />} />
          <Route path="/sla" element={<Sla />} />
          <Route path="/analytics" element={<Analytics />} />
          <Route path="/integrations" element={<Integrations />} />
          <Route path="/sources" element={<Sources />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/change-history" element={<ChangeHistory />} />
          <Route path="/demo" element={<Demo />} />
        </Routes>
      </main>
    </div>
  );
}
