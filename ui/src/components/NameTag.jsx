import { BsChevronRight } from "react-icons/bs";
import { Button } from "react-bootstrap";

export default function NameTag({ node, onSelect }) {
    // react-arborist's hidden root sits above "My Files"/"Trash"; it has no
    // name, so stop there instead of rendering an empty crumb and a chevron.
    if (node.parent && node.parent.id !== '__REACT_ARBORIST_INTERNAL_ROOT__') {
        return (<>
            <NameTag node={node.parent} onSelect={onSelect} />
            <BsChevronRight />
            <Button variant="outline" onClick={() => onSelect(node)}>{node.data.name}</Button>
        </>)
    }
    // No parent means this is the root - render it
    return <Button variant="outline" onClick={() => onSelect(node)}>{node.data.name}</Button>
}