import { createFileRoute } from "@tanstack/react-router";
import { PlanRunView } from "../components/plan/PlanRunView";

export const Route = createFileRoute("/_shell/projects/$projectId_/plans/$planId")({
	component: PlanRunRoute,
});

function PlanRunRoute() {
	const { projectId, planId } = Route.useParams();
	return <PlanRunView projectId={projectId} planId={planId} />;
}
